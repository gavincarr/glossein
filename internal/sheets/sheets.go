// Package sheets fetches and parses publicly-shared Google Sheets as CSV.
//
// Sheets must be shared with "Anyone with the link" to be readable, since
// the gviz CSV endpoint is unauthenticated. Private sheets return an HTML
// login page that this package detects and reports as ErrNotPublic.
package sheets

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrNotPublic   = errors.New("sheet is not publicly accessible (Share → Anyone with the link → Viewer)")
	ErrEmptyColumn = errors.New("no non-empty values found in target column")
)

var (
	sheetIDInURL  = regexp.MustCompile(`/spreadsheets/d/([a-zA-Z0-9_-]+)`)
	rawIDPattern  = regexp.MustCompile(`^[a-zA-Z0-9_-]{20,}$`)
	letterPattern = regexp.MustCompile(`^[A-Za-z]+$`)
)

// ExtractID returns the spreadsheet ID from a Google Sheets sharing URL,
// an /edit URL, or a bare 20+ character ID.
func ExtractID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("empty sheet URL or ID")
	}
	if m := sheetIDInURL.FindStringSubmatch(input); m != nil {
		return m[1], nil
	}
	if rawIDPattern.MatchString(input) {
		return input, nil
	}
	return "", fmt.Errorf("could not extract Google Sheets ID from %q", input)
}

// csvURL returns the unauthenticated CSV-export endpoint for a sheet.
func csvURL(id string) string {
	return "https://docs.google.com/spreadsheets/d/" + id + "/gviz/tq?tqx=out:csv"
}

// FetchCSV retrieves the first tab of a public sheet as CSV bytes.
// Returns ErrNotPublic if Google serves an HTML login page instead of CSV.
func FetchCSV(ctx context.Context, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, csvURL(id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching sheet: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading sheet response: %w", err)
	}

	return interpretCSVResponse(resp.StatusCode, resp.Header.Get("Content-Type"), body)
}

// interpretCSVResponse applies FetchCSV's classification rules to an already-read response.
// Split out for unit-testability.
func interpretCSVResponse(status int, contentType string, body []byte) ([]byte, error) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, ErrNotPublic
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		return nil, ErrNotPublic
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP %d from Google Sheets", status)
	}
	return body, nil
}

// HeaderOf returns the trimmed header cell (row 1) for the given column spec.
// Returns "" if the cell is absent or empty. The spec accepts the same forms
// as ParseColumn, though "#Header Name" simply returns that name back.
func HeaderOf(csvData []byte, column string) (string, error) {
	reader := csv.NewReader(bytes.NewReader(csvData))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("parsing CSV: %w", err)
	}
	if len(records) == 0 {
		return "", nil
	}
	colIdx, err := resolveColumn(column, records[0])
	if err != nil {
		return "", err
	}
	if colIdx >= len(records[0]) {
		return "", nil
	}
	return strings.TrimSpace(records[0][colIdx]), nil
}

// ParseColumn extracts non-empty trimmed values from one column of the CSV.
//
// column may be a letter spec ("A", "B", ..., "AA"), a 1-based number ("1",
// "2", ...), or "#Header Name" for case-insensitive header lookup in row 1.
// When skipHeader is true, row 1 is excluded from the results.
func ParseColumn(csvData []byte, column string, skipHeader bool) ([]string, error) {
	reader := csv.NewReader(bytes.NewReader(csvData))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, ErrEmptyColumn
	}

	colIdx, err := resolveColumn(column, records[0])
	if err != nil {
		return nil, err
	}

	maxCols := 0
	for _, r := range records {
		if len(r) > maxCols {
			maxCols = len(r)
		}
	}
	if colIdx >= maxCols {
		return nil, fmt.Errorf("column %q resolves to index %d, but sheet has only %d column(s)", column, colIdx, maxCols)
	}

	start := 0
	if skipHeader {
		start = 1
	}

	var out []string
	for _, r := range records[start:] {
		if colIdx >= len(r) {
			continue
		}
		v := strings.TrimSpace(r[colIdx])
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, ErrEmptyColumn
	}
	return out, nil
}

func resolveColumn(spec string, header []string) (int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, errors.New("empty column spec")
	}

	if strings.HasPrefix(spec, "#") {
		name := strings.ToLower(strings.TrimSpace(spec[1:]))
		if name == "" {
			return 0, errors.New(`empty header name after "#"`)
		}
		for i, h := range header {
			if strings.ToLower(strings.TrimSpace(h)) == name {
				return i, nil
			}
		}
		return 0, fmt.Errorf("header %q not found in row 1: got %v", name, header)
	}

	if n, err := strconv.Atoi(spec); err == nil {
		if n < 1 {
			return 0, fmt.Errorf("column number must be >= 1, got %d", n)
		}
		return n - 1, nil
	}

	if letterPattern.MatchString(spec) {
		return letterToIndex(spec), nil
	}

	return 0, fmt.Errorf("invalid column spec %q: expected letter (A, B, ...), 1-based number, or #Header", spec)
}

func letterToIndex(s string) int {
	s = strings.ToUpper(s)
	idx := 0
	for _, c := range s {
		idx = idx*26 + int(c-'A'+1)
	}
	return idx - 1
}
