package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

//go:embed flashcards.html
var flashcardsHTML []byte

func main() {
	port := flag.Int("port", 8080, "port to listen on")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/csv", handleCSV)

	addr := fmt.Sprintf("localhost:%d", *port)
	fmt.Printf("Flashcards server listening on http://%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(flashcardsHTML)
}

func handleCSV(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	if raw == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}
	if !strings.HasSuffix(u.Host, "docs.google.com") && !strings.HasSuffix(u.Host, "googleusercontent.com") {
		http.Error(w, "only docs.google.com / googleusercontent.com URLs are allowed", http.StatusBadRequest)
		return
	}
	resp, err := http.Get(raw)
	if err != nil {
		http.Error(w, "fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	io.Copy(w, resp.Body)
}
