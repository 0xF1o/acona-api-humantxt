package vm_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"acona-api-humantxt/internal/fetch"
	"acona-api-humantxt/internal/vm"
)

func newEngine(t *testing.T) *vm.Engine {
	t.Helper()

	// httptest servers live on 127.0.0.1, so the SSRF guard has to be lifted
	engine, err := vm.New(2, fetch.New(fetch.Options{AllowPrivate: true}))
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	t.Cleanup(engine.Close)

	return engine
}

func fixture(t *testing.T, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", name+".html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return string(raw)
}

// TestExtractMatchesParser compares the QuickJS run against output the very same
// parser produced under Node (js/golden.mjs regenerates those files).
func TestExtractMatchesParser(t *testing.T) {
	engine := newEngine(t)

	for _, fixtureName := range []string{"article", "empty"} {
		for _, format := range []string{"text", "markdown", "html"} {
			name := fmt.Sprintf("%s as %s", fixtureName, format)

			t.Run(name, func(t *testing.T) {
				golden, err := os.ReadFile(filepath.Join("testdata", "golden", fixtureName+"."+format+".json"))
				if err != nil {
					t.Fatalf("read golden: %v", err)
				}

				var want vm.Article
				if err := json.Unmarshal(golden, &want); err != nil {
					t.Fatalf("decode golden: %v", err)
				}

				url := "https://example.com/news/story/"
				if fixtureName == "empty" {
					url = "https://example.com/empty/"
				}

				got, err := engine.Extract(t.Context(), url, fixture(t, fixtureName), format)
				if err != nil {
					t.Fatalf("extract: %v", err)
				}

				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				wantJSON, _ := json.MarshalIndent(&want, "", "  ")

				if string(gotJSON) != string(wantJSON) {
					t.Errorf("parser output differs:\n got: %s\nwant: %s", gotJSON, wantJSON)
				}
			})
		}
	}
}

// TestArticleCoversParserFields keeps the response type honest: if the parser
// ever answers with a field we do not model, this tells us instead of dropping it.
func TestArticleCoversParserFields(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "golden", "article.text.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(golden, &fields); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	parserFields := reflect.TypeOf(vm.Article{})
	known := map[string]bool{}

	for i := 0; i < parserFields.NumField(); i++ {
		known[strings.Split(parserFields.Field(i).Tag.Get("json"), ",")[0]] = true
	}

	for field := range fields {
		if !known[field] {
			t.Errorf("parser returns %q, which vm.Article does not model", field)
		}
	}
}

func TestExtractWithoutURL(t *testing.T) {
	engine := newEngine(t)

	article, err := engine.Extract(t.Context(), "", fixture(t, "article"), "text")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if article.Title != "Extraction works" {
		t.Errorf("title = %q", article.Title)
	}

	if article.URL != "" || article.Domain != "" {
		t.Errorf("url/domain = %q/%q, want both empty", article.URL, article.Domain)
	}

	if !strings.Contains(article.Content, "first paragraph") {
		t.Errorf("content = %q", article.Content)
	}
}

// TestExtractPaginatedArticle covers the requests the parser makes on its own:
// page two is fetched through the Go host, not by Node.
func TestExtractPaginatedArticle(t *testing.T) {
	firstPage := fixture(t, "page1")
	secondPage := fixture(t, "page2")

	var requested []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		if r.URL.Path == "/story/2" {
			w.Write([]byte(secondPage))
			return
		}

		w.Write([]byte(firstPage))
	}))
	defer srv.Close()

	engine := newEngine(t)

	article, err := engine.Extract(t.Context(), srv.URL+"/story/1", "", "text")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if article.TotalPages != 2 || article.RenderedPages != 2 {
		t.Errorf("total/rendered pages = %d/%d, want 2/2 (fetched: %v)", article.TotalPages, article.RenderedPages, requested)
	}

	if !strings.Contains(article.Content, "Page two opens with a sentence") {
		t.Errorf("second page missing from content: %q", article.Content)
	}

	if !strings.Contains(article.Content, "first paragraph") {
		t.Errorf("first page missing from content: %q", article.Content)
	}
}

func TestExtractUnreachableURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	url := srv.URL
	srv.Close()

	engine := newEngine(t)

	err := error(nil)
	if _, err = engine.Extract(t.Context(), url, "", "text"); err == nil {
		t.Fatal("want an error for a url that cannot be fetched")
	}
}

func TestExtractConcurrent(t *testing.T) {
	engine := newEngine(t)
	source := fixture(t, "article")

	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			article, err := engine.Extract(t.Context(), "https://example.com/news/story/", source, "markdown")
			if err != nil {
				t.Errorf("extract: %v", err)
				return
			}

			if article.WordCount < 1 {
				t.Error("empty article")
			}
		}()
	}

	wg.Wait()
}

// TestRuntimeSurvivesParserErrors keeps the pool from throwing a runtime away
// every time the parser simply refuses a document.
func TestRuntimeSurvivesParserErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	engine := newEngine(t)

	for range 3 {
		if _, err := engine.Extract(t.Context(), srv.URL, "", "text"); err == nil {
			t.Fatal("want an error for a 404")
		}
	}

	article, err := engine.Extract(t.Context(), "", fixture(t, "article"), "text")
	if err != nil {
		t.Fatalf("extract after failures: %v", err)
	}

	if article.Title != "Extraction works" {
		t.Errorf("title = %q", article.Title)
	}
}

// blockingFetcher holds a runtime for as long as the test wants it.
type blockingFetcher struct{ release chan struct{} }

func (f blockingFetcher) Fetch(string, map[string]string) (*fetch.Reply, error) {
	<-f.release

	return nil, errors.New("released")
}

// TestBusyEngineTurnsRequestersAway keeps a full pool from queueing requests
// until they time out on the client side.
func TestBusyEngineTurnsRequestersAway(t *testing.T) {
	release := make(chan struct{})
	engine, err := vm.New(1, blockingFetcher{release})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer engine.Close()

	engine.QueueWait = 50 * time.Millisecond

	go func() {
		_, _ = engine.Extract(t.Context(), "https://example.com", "", "text")
	}()

	time.Sleep(200 * time.Millisecond)

	if _, err := engine.Extract(t.Context(), "https://example.com", "", "text"); !errors.Is(err, vm.ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}

	close(release)
}

// BenchmarkExtraction is the "here" column of the table in the README:
// go test -run '^$' -bench Extraction -benchtime 3x ./internal/vm/
func BenchmarkExtraction(b *testing.B) {
	engine, err := vm.New(1, fetch.New(fetch.Options{AllowPrivate: true}))
	if err != nil {
		b.Fatalf("new engine: %v", err)
	}
	defer engine.Close()

	for _, size := range []int{100_000, 250_000, 500_000} {
		b.Run(fmt.Sprintf("%d KB", size/1000), func(b *testing.B) {
			html := strings.Replace("<html><head><title>Big</title></head><body><article><h1>Big</h1>%s</article></body></html>", "%s", strings.Repeat("<p>Sentence about lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor.</p>", size/106), 1)

			for range b.N {
				if _, err := engine.Extract(b.Context(), "", html, "text"); err != nil {
					b.Fatalf("extract: %v", err)
				}
			}
		})
	}
}
