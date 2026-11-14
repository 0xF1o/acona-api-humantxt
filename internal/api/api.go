// Package api implements the HTTP interface: GET with a url parameter (the
// historical API) and POST with the document itself as request body.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"acona-api-humantxt/internal/fetch"
	"acona-api-humantxt/internal/vm"
)

// Formats are the accepted values for the format parameter.
var Formats = []string{"text", "markdown", "html"}

var errBodyTooLarge = fmt.Errorf("Request body too large, documents are limited to %d bytes", fetch.MaxDocument)

// Extractor turns a url or a document into an article.
type Extractor interface {
	Extract(ctx context.Context, rawURL, html, format string) (*vm.Article, error)
}

// Server handles extraction requests.
type Server struct {
	Engine  Extractor
	MaxBody int64
}

func New(engine Extractor) *Server {
	return &Server{Engine: engine, MaxBody: fetch.MaxDocument}
}

// input is the accepted request, from query parameters or from a POST body.
type input struct {
	URL    string `json:"url"`
	HTML   string `json:"html"`
	Format string `json:"format"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	in, err := s.readInput(w, r)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}

	if in.Format == "" {
		in.Format = "text"
	}

	if !slices.Contains(Formats, in.Format) {
		http.Error(w, fmt.Sprintf("Invalid format: '%v'", in.Format), http.StatusBadRequest)
		return
	}

	target := ""

	if in.URL != "" {
		normalized, err := fetch.ParseURL(in.URL)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid url: '%v'", in.URL), http.StatusBadRequest)
			return
		}
		target = normalized.String()
	}

	if target == "" && in.HTML == "" {
		http.Error(w, "Parameter 'url' not found", http.StatusBadRequest)
		return
	}

	article, err := s.Engine.Extract(r.Context(), target, in.HTML, in.Format)
	if err != nil {
		s.failed(w, err)
		return
	}

	writeJSON(w, article)
}

// failed reports why a request was not answered. A document the parser declines
// is answered the way the CLI this service replaced answered it: with the
// parser's own error object as the result. Everything else that is the caller's
// fault is a bad request, as it was before; the two ways this service itself can
// fail are a full queue and a runtime that gave up.
func (s *Server) failed(w http.ResponseWriter, err error) {
	var declined *vm.ParserError

	switch {
	case errors.As(err, &declined):
		writeJSON(w, map[string]any{"error": true, "message": declined.Message})
	case errors.Is(err, vm.ErrBusy):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// writeJSON answers with indented JSON, as the CLI based service did.
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(value)
}

// readInput collects the document and its options from a JSON body, a raw HTML
// body or - for GET - from the query string.
func (s *Server) readInput(w http.ResponseWriter, r *http.Request) (in input, err error) {
	query := r.URL.Query()

	if r.Method != http.MethodPost {
		return input{URL: query.Get("url"), Format: query.Get("format")}, nil
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBody)

	if isJSON(r.Header.Get("Content-Type")) {
		err = json.NewDecoder(r.Body).Decode(&in)
	} else {
		var raw []byte
		raw, err = io.ReadAll(r.Body)
		in.HTML = string(raw)
	}

	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return in, errBodyTooLarge
		}

		return in, fmt.Errorf("Invalid request body: %v", err)
	}

	if in.URL == "" {
		in.URL = query.Get("url")
	}

	if in.Format == "" {
		in.Format = query.Get("format")
	}

	return in, nil
}

func isJSON(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	return media == "application/json" || strings.HasSuffix(media, "+json")
}
