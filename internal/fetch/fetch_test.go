package fetch_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"acona-api-humantxt/internal/fetch"
)

func testFetcher() *fetch.Fetcher {
	// httptest servers live on 127.0.0.1, so the SSRF guard has to be lifted
	return fetch.New(fetch.Options{AllowPrivate: true})
}

func TestFetchReturnsResponse(t *testing.T) {
	var agent, accept string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, accept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html><body><h1>Hallo</h1></body></html>"))
	}))
	defer srv.Close()

	reply, err := testFetcher().Fetch(srv.URL, nil)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if reply.Status != 200 || reply.StatusText != "OK" {
		t.Errorf("status = %d/%q, want 200/OK", reply.Status, reply.StatusText)
	}

	if !strings.Contains(string(reply.Body), "Hallo") {
		t.Errorf("body = %q", reply.Body)
	}

	if got := reply.Headers["content-type"]; !strings.Contains(got, "text/html") {
		t.Errorf("content type header = %q", got)
	}

	if !strings.Contains(agent, "HumanTXT") {
		t.Errorf("user agent = %q", agent)
	}

	if !strings.Contains(accept, "text/html") {
		t.Errorf("accept = %q", accept)
	}
}

func TestFetchKeepsCallerHeaders(t *testing.T) {
	var agent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.Header.Get("User-Agent")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	if _, err := testFetcher().Fetch(srv.URL, map[string]string{"user-agent": "Mozilla/5.0 (Windows NT 6.1)"}); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if agent != "Mozilla/5.0 (Windows NT 6.1)" {
		t.Errorf("user agent = %q, want the one the parser passed", agent)
	}
}

func TestFetchFollowsRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.Write([]byte("arrived"))
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer srv.Close()

	reply, err := testFetcher().Fetch(srv.URL+"/start", nil)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if string(reply.Body) != "arrived" {
		t.Errorf("body = %q", reply.Body)
	}
}

func TestFetchReportsStatusInsteadOfFailing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	reply, err := testFetcher().Fetch(srv.URL, nil)
	if err != nil {
		t.Fatalf("the parser decides what a status code means, fetch should not fail: %v", err)
	}

	if reply.Status != 404 {
		t.Errorf("status = %d, want 404", reply.Status)
	}
}

func TestFetchLimitsBodySize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	_, err := fetch.New(fetch.Options{AllowPrivate: true, MaxBytes: 64}).Fetch(srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "larger than the 64 byte document limit") {
		t.Fatalf("err = %v, want a size error", err)
	}
}

func TestFetchRejectsNonPublicAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	_, err := fetch.New(fetch.Options{}).Fetch(srv.URL, nil)
	if !errors.Is(err, fetch.ErrBlockedAddress) {
		t.Fatalf("err = %v, want %v", err, fetch.ErrBlockedAddress)
	}
}

func TestParseURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://orf.at/story", "https://orf.at/story"},
		{"http://example.com/a?b=1", "http://example.com/a?b=1"},
		{"orf.at", "https://orf.at"},
		{"www.orf.at/", "https://www.orf.at/"},
	} {
		got, err := fetch.ParseURL(tc.in)
		if err != nil {
			t.Errorf("ParseURL(%q): %v", tc.in, err)
			continue
		}

		if got.String() != tc.want {
			t.Errorf("ParseURL(%q) = %q, want %q", tc.in, got.String(), tc.want)
		}
	}

	for _, invalid := range []string{"", "ftp://example.com", "https://exa mple", "https://", "not a url at all"} {
		if got, err := fetch.ParseURL(invalid); err == nil {
			t.Errorf("ParseURL(%q) = %q, want an error", invalid, got)
		}
	}
}

func TestFetchRefusesPortsThatAreNotWebPorts(t *testing.T) {
	for _, target := range []string{"http://example.com:25/", "https://example.com:6379/"} {
		if _, err := fetch.New(fetch.Options{}).Fetch(target, nil); err == nil || !strings.Contains(err.Error(), "port") {
			t.Errorf("%s: err = %v, want a refused port", target, err)
		}
	}
}

func TestFetchRefusesCarrierGradeAddresses(t *testing.T) {
	// 100.64.0.0/10 is what mesh networks hand out; it passes a plain privacy test
	for _, target := range []string{"http://100.64.0.1/", "http://198.18.0.1/"} {
		if _, err := fetch.New(fetch.Options{}).Fetch(target, nil); !errors.Is(err, fetch.ErrBlockedAddress) {
			t.Errorf("%s: err = %v, want %v", target, err, fetch.ErrBlockedAddress)
		}
	}
}
