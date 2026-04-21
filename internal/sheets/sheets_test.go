package sheets

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExtractID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://docs.google.com/spreadsheets/d/12TWGqpozKTFMBuqE96bUbZIYDtL__VeEOVK87NEERLE/edit?usp=sharing", "12TWGqpozKTFMBuqE96bUbZIYDtL__VeEOVK87NEERLE", false},
		{"https://docs.google.com/spreadsheets/d/abc123XYZ_-abc123XYZ/edit#gid=0", "abc123XYZ_-abc123XYZ", false},
		{"  https://docs.google.com/spreadsheets/d/1xYz/ ", "1xYz", false},
		{"12TWGqpozKTFMBuqE96bUbZIYDtL__VeEOVK87NEERLE", "12TWGqpozKTFMBuqE96bUbZIYDtL__VeEOVK87NEERLE", false},
		{"short", "", true},
		{"", "", true},
		{"not a url or id!!!", "", true},
	}
	for _, tc := range cases {
		got, err := ExtractID(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ExtractID(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("ExtractID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseColumn_LetterAndNumber(t *testing.T) {
	csv := []byte("Prompt,Answer,Notes\nhello,ciao,greeting\nbye,arrivederci,\n,empty prompt gets skipped,\ngoodnight,buonanotte,\n")

	got, err := ParseColumn(csv, "B", true)
	if err != nil {
		t.Fatalf("ParseColumn B: %v", err)
	}
	want := []string{"ciao", "arrivederci", "empty prompt gets skipped", "buonanotte"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("col B = %v, want %v", got, want)
	}

	got, err = ParseColumn(csv, "2", true)
	if err != nil {
		t.Fatalf("ParseColumn 2: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("col 2 = %v, want %v", got, want)
	}

	got, err = ParseColumn(csv, "A", true)
	if err != nil {
		t.Fatalf("ParseColumn A: %v", err)
	}
	wantA := []string{"hello", "bye", "goodnight"}
	if !reflect.DeepEqual(got, wantA) {
		t.Errorf("col A = %v, want %v", got, wantA)
	}
}

func TestParseColumn_HeaderName(t *testing.T) {
	csv := []byte("L1,L2,Audio\nhello,ciao,ciao!\nbye,arrivederci,arrivederci!\n")

	got, err := ParseColumn(csv, "#Audio", true)
	if err != nil {
		t.Fatalf("ParseColumn #Audio: %v", err)
	}
	want := []string{"ciao!", "arrivederci!"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("#Audio = %v, want %v", got, want)
	}

	if _, err := ParseColumn(csv, "#audio", true); err != nil {
		t.Errorf("case-insensitive lookup failed: %v", err)
	}

	if _, err := ParseColumn(csv, "#NotThere", true); err == nil {
		t.Errorf("expected error for missing header")
	}
}

func TestParseColumn_OutOfRange(t *testing.T) {
	csv := []byte("A,B,C\n1,2,3\n")
	_, err := ParseColumn(csv, "E", true)
	if err == nil {
		t.Fatal("expected error for out-of-range column")
	}
	if !strings.Contains(err.Error(), "only 3") {
		t.Errorf("error should mention column count: %v", err)
	}
}

func TestParseColumn_SkipHeaderFalse(t *testing.T) {
	csv := []byte("a,b\nc,d\n")
	got, err := ParseColumn(csv, "A", false)
	if err != nil {
		t.Fatalf("ParseColumn: %v", err)
	}
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skipHeader=false: got %v, want %v", got, want)
	}
}

func TestParseColumn_QuotedAndCRLF(t *testing.T) {
	// Row 2 has a quoted cell with comma and escaped quote. CRLF line endings.
	csv := []byte("p,a\r\n\"hello, \"\"world\"\"\",ciao\r\nbye,arrivederci\r\n")
	got, err := ParseColumn(csv, "A", true)
	if err != nil {
		t.Fatalf("ParseColumn: %v", err)
	}
	want := []string{`hello, "world"`, "bye"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseColumn_EmptyAfterSkip(t *testing.T) {
	csv := []byte("only,header\n")
	_, err := ParseColumn(csv, "A", true)
	if !errors.Is(err, ErrEmptyColumn) {
		t.Errorf("expected ErrEmptyColumn, got %v", err)
	}
}

func TestParseColumn_LetterAA(t *testing.T) {
	// 27 columns: A..Z, AA. Check that "AA" resolves to index 26.
	header := strings.Join([]string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z", "AA"}, ",")
	row := strings.Repeat("x,", 26) + "target"
	csv := []byte(header + "\n" + row + "\n")

	got, err := ParseColumn(csv, "AA", true)
	if err != nil {
		t.Fatalf("ParseColumn AA: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"target"}) {
		t.Errorf("AA = %v, want [target]", got)
	}
}

func TestHeaderOf(t *testing.T) {
	csv := []byte("EN,IT,Notes\nhello,ciao,greeting\n")

	cases := []struct {
		col  string
		want string
	}{
		{"A", "EN"},
		{"B", "IT"},
		{"2", "IT"},
		{"C", "Notes"},
	}
	for _, tc := range cases {
		got, err := HeaderOf(csv, tc.col)
		if err != nil {
			t.Errorf("HeaderOf(%q): %v", tc.col, err)
			continue
		}
		if got != tc.want {
			t.Errorf("HeaderOf(%q) = %q, want %q", tc.col, got, tc.want)
		}
	}

	// Empty CSV → empty string, no error
	got, err := HeaderOf([]byte(""), "A")
	if err != nil || got != "" {
		t.Errorf("empty CSV: got (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestLetterToIndex(t *testing.T) {
	cases := map[string]int{
		"A": 0, "B": 1, "Z": 25, "AA": 26, "AB": 27, "AZ": 51, "BA": 52,
	}
	for in, want := range cases {
		if got := letterToIndex(in); got != want {
			t.Errorf("letterToIndex(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestInterpretCSVResponse(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		contentType string
		body        []byte
		wantErr     error
		wantBody    []byte
	}{
		{"ok csv", 200, "text/csv; charset=utf-8", []byte("a,b\n1,2\n"), nil, []byte("a,b\n1,2\n")},
		{"403 private", 403, "text/html", []byte("<html/>"), ErrNotPublic, nil},
		{"401 private", 401, "text/html", []byte("<html/>"), ErrNotPublic, nil},
		{"200 html login page", 200, "text/html; charset=utf-8", []byte("<!DOCTYPE html>..."), ErrNotPublic, nil},
		{"200 html with leading whitespace", 200, "application/octet-stream", []byte("   <html>..."), ErrNotPublic, nil},
		{"500 unexpected", 500, "text/plain", []byte("boom"), nil, nil}, // expects non-sentinel error
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := interpretCSVResponse(tc.status, tc.contentType, tc.body)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if tc.wantBody == nil && err == nil {
				t.Errorf("expected some error, got nil")
				return
			}
			if tc.wantBody != nil {
				if err != nil {
					t.Errorf("unexpected err: %v", err)
				}
				if !reflect.DeepEqual(body, tc.wantBody) {
					t.Errorf("body = %q, want %q", body, tc.wantBody)
				}
			}
		})
	}
}
