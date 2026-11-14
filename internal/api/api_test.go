package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"acona-api-humantxt/internal/api"
	"acona-api-humantxt/internal/vm"
)

type stubEngine struct {
	article         *vm.Article
	err             error
	url, html, form string
	calls           int
}

func (s *stubEngine) Extract(_ context.Context, rawURL, html, format string) (*vm.Article, error) {
	s.url, s.html, s.form, s.calls = rawURL, html, format, s.calls+1
	return s.article, s.err
}

func newServer() (*api.Server, *stubEngine) {
	engine := &stubEngine{article: &vm.Article{
		Title: "T", Content: "C", WordCount: 3, Direction: "ltr",
		URL: "https://orf.at/story", Domain: "orf.at", TotalPages: 1, RenderedPages: 1,
	}}

	return api.New(engine), engine
}

func do(handler http.Handler, method, target, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestGetWithExternalURL(t *testing.T) {
	handler, engine := newServer()

	rec := do(handler, http.MethodGet, "/?url=https://orf.at/story", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body)
	}

	if engine.url != "https://orf.at/story" || engine.html != "" || engine.form != "text" {
		t.Errorf("parser called with url=%q html=%q format=%q", engine.url, engine.html, engine.form)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
}

func TestResponseShapeMatchesParser(t *testing.T) {
	handler, _ := newServer()

	rec := do(handler, http.MethodGet, "/?url=https://orf.at/story", "", "")

	want := `{
  "title": "T",
  "author": null,
  "date_published": null,
  "dek": null,
  "lead_image_url": null,
  "content": "C",
  "next_page_url": null,
  "url": "https://orf.at/story",
  "domain": "orf.at",
  "excerpt": "",
  "word_count": 3,
  "direction": "ltr",
  "total_pages": 1,
  "rendered_pages": 1
}
`
	if rec.Body.String() != want {
		t.Errorf("body =\n%s\nwant\n%s", rec.Body.String(), want)
	}
}

func TestBareDomainIsRequestedOverHTTPS(t *testing.T) {
	handler, engine := newServer()

	if rec := do(handler, http.MethodGet, "/?url=orf.at", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body)
	}

	if engine.url != "https://orf.at" {
		t.Errorf("url = %q, want %q", engine.url, "https://orf.at")
	}
}

func TestPostHTMLBody(t *testing.T) {
	handler, engine := newServer()

	rec := do(handler, http.MethodPost, "/?format=markdown", "<h1>posted</h1>", "text/html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body)
	}

	if engine.html != "<h1>posted</h1>" || engine.form != "markdown" || engine.url != "" {
		t.Errorf("parser called with url=%q html=%q format=%q", engine.url, engine.html, engine.form)
	}
}

func TestPostJSONBody(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantHTML, wantURL, wantFormat string
	}{
		{"html only", `{"html":"<p>hi</p>"}`, "<p>hi</p>", "", "text"},
		{"html and format", `{"html":"<p>hi</p>","format":"html"}`, "<p>hi</p>", "", "html"},
		{"url only", `{"url":"https://orf.at"}`, "", "https://orf.at", "text"},
		{"html url and format", `{"url":"https://orf.at","html":"<p>hi</p>","format":"markdown"}`, "<p>hi</p>", "https://orf.at", "markdown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, engine := newServer()

			rec := do(handler, http.MethodPost, "/", tc.body, "application/json")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %q", rec.Code, rec.Body)
			}

			if engine.html != tc.wantHTML || engine.url != tc.wantURL || engine.form != tc.wantFormat {
				t.Errorf("parser called with url=%q html=%q format=%q", engine.url, engine.html, engine.form)
			}
		})
	}
}

func TestBadRequests(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body, contentType, wantBody string
	}{
		{"missing url", http.MethodGet, "/", "", "", "Parameter 'url' not found"},
		{"empty url", http.MethodGet, "/?url=", "", "", "Parameter 'url' not found"},
		{"unknown format", http.MethodGet, "/?url=https://orf.at&format=json", "", "", "Invalid format: 'json'"},
		{"invalid url", http.MethodGet, "/?url=ftp://orf.at", "", "", "Invalid url: 'ftp://orf.at'"},
		{"empty post body", http.MethodPost, "/", "", "text/html", "Parameter 'url' not found"},
		{"broken json", http.MethodPost, "/", `{"html":`, "application/json", "Invalid request body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, engine := newServer()

			rec := do(handler, tc.method, tc.target, tc.body, tc.contentType)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body)
			}

			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want %q", rec.Body, tc.wantBody)
			}

			if engine.calls != 0 {
				t.Errorf("the parser should not have run")
			}
		})
	}
}

func TestOversizedPostBody(t *testing.T) {
	handler, _ := newServer()
	handler.MaxBody = 16

	rec := do(handler, http.MethodPost, "/", strings.Repeat("x", 32), "text/html")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body = %q, want 413", rec.Code, rec.Body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	handler, _ := newServer()

	rec := do(handler, http.MethodPut, "/", "", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}

	if rec.Header().Get("Allow") != "GET, POST" {
		t.Errorf("allow = %q", rec.Header().Get("Allow"))
	}
}

func TestParserFailureIsBadRequest(t *testing.T) {
	handler, engine := newServer()
	engine.err = errors.New("Resource returned a response status code of 404")

	rec := do(handler, http.MethodGet, "/?url=https://orf.at", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 as before", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "status code of 404") {
		t.Errorf("body = %q", rec.Body)
	}
}

func TestHeadRequest(t *testing.T) {
	handler, engine := newServer()

	rec := do(handler, http.MethodHead, "/?url=https://orf.at", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if engine.calls != 1 {
		t.Errorf("parser calls = %d, want 1", engine.calls)
	}

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Errorf("body = %q, want json", rec.Body)
	}
}

// TestDeclinedDocumentAnswersLikeTheCLI pins the behaviour clients of the old
// service saw: the parser's own error object, as a normal result.
func TestDeclinedDocumentAnswersLikeTheCLI(t *testing.T) {
	handler, engine := newServer()
	engine.err = &vm.ParserError{Message: "Resource returned a response status code of 404"}

	rec := do(handler, http.MethodGet, "/?url=https://orf.at/missing", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 like the CLI", rec.Code)
	}

	want := `{
  "error": true,
  "message": "Resource returned a response status code of 404"
}
`
	if rec.Body.String() != want {
		t.Errorf("body =\n%s\nwant\n%s", rec.Body.String(), want)
	}
}

func TestBusyEngineAnswers503(t *testing.T) {
	handler, engine := newServer()
	engine.err = vm.ErrBusy

	rec := do(handler, http.MethodGet, "/?url=https://orf.at", "", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestOversizedBodyAnswers413(t *testing.T) {
	handler, _ := newServer()

	rec := do(handler, http.MethodPost, "/", strings.Repeat("<p>x</p>", 300_000), "text/html")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "too large") {
		t.Errorf("body = %q", rec.Body.String())
	}
}
