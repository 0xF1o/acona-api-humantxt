// Package vm runs the extraction bundle (js/, built into internal/vm/bundle)
// inside QuickJS compiled to WebAssembly: Postlight Parser with no Node.js and
// no child processes.
package vm

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fastschema/qjs"

	"acona-api-humantxt/internal/fetch"
)

//go:embed bundle/humantxt.js
var bundle string

// Article is what the parser returns. Field names, order and null-ness are the
// parser's own, so clients of the previous CLI based service keep working.
type Article struct {
	Title         string  `json:"title"`
	Author        *string `json:"author"`
	DatePublished *string `json:"date_published"`
	Dek           *string `json:"dek"`
	LeadImageURL  *string `json:"lead_image_url"`
	Content       string  `json:"content"`
	NextPageURL   *string `json:"next_page_url"`
	URL           string  `json:"url"`
	Domain        string  `json:"domain"`
	Excerpt       string  `json:"excerpt"`
	WordCount     int     `json:"word_count"`
	Direction     string  `json:"direction"`
	TotalPages    int     `json:"total_pages"`
	RenderedPages int     `json:"rendered_pages"`
}

// Fetcher downloads what the parser asks for. The parser fetches the url of a
// request and page 2 and beyond of a paginated article.
type Fetcher interface {
	Fetch(rawURL string, headers map[string]string) (*fetch.Reply, error)
}

// ErrBusy means every runtime is taken and the wait for one ran out.
var ErrBusy = errors.New("all extraction runtimes are busy, try again later")

// Engine is a pool of QuickJS runtimes, safe for concurrent use.
type Engine struct {
	// QueueWait bounds how long a request waits for a free runtime before it is
	// turned away with ErrBusy.
	QueueWait time.Duration
	code      []byte
	pool      *qjs.Pool
	fetcher   Fetcher
	sem       chan struct{}
	mu        sync.Mutex
	runtimes  []*qjs.Runtime
}

// New creates the runtime pool and verifies the bundle evaluates. size bounds
// both the number of runtimes and the number of concurrent extractions.
func New(size int, fetcher Fetcher) (*Engine, error) {
	if size < 1 {
		size = 1
	}

	if fetcher == nil {
		return nil, errors.New("an engine needs a fetcher: the parser fetches pages itself")
	}

	code, err := compile()
	if err != nil {
		return nil, err
	}

	e := &Engine{code: code, fetcher: fetcher, sem: make(chan struct{}, size), QueueWait: 15 * time.Second}
	e.pool = qjs.NewPool(size, qjs.Option{MemoryLimit: 192 << 20}, e.setup)

	rt, err := e.pool.Get()
	if err != nil {
		return nil, err
	}
	e.pool.Put(rt)

	return e, nil
}

func (e *Engine) setup(rt *qjs.Runtime) error {
	ctx := rt.Context()
	ctx.SetFunc("__fetch", e.fetch)

	// the pool frees a runtime whose setup function failed
	if _, err := ctx.Eval("humantxt.js", qjs.Bytecode(e.code)); err != nil {
		return fmt.Errorf("load extraction bundle: %w", err)
	}

	e.mu.Lock()
	e.runtimes = append(e.runtimes, rt)
	e.mu.Unlock()

	return nil
}

// compile turns the bundle into QuickJS bytecode once, which makes every runtime
// after the first one start roughly three times faster.
func compile() ([]byte, error) {
	rt, err := qjs.New()
	if err != nil {
		return nil, fmt.Errorf("start javascript runtime: %w", err)
	}
	defer closeRuntime(rt)

	code, err := rt.Context().Compile("humantxt.js", qjs.Code(bundle))
	if err != nil {
		return nil, fmt.Errorf("compile extraction bundle: %w", err)
	}

	return code, nil
}

// reply is the shape shim-request.js expects from __fetch.
type reply struct {
	Status     int               `json:"status,omitempty"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// fetch answers the parser's requests. Failures travel back as a value instead
// of a thrown error so the parser's own error handling stays in charge.
func (e *Engine) fetch(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	if len(args) < 2 {
		return this.Context().NewString(`{"error":"__fetch expects a url and its headers"}`), nil
	}

	var headers map[string]string
	// unparsable headers stay empty: the parser's own defaults then apply
	if err := json.Unmarshal([]byte(args[1].String()), &headers); err != nil {
		headers = nil
	}

	out, err := e.fetcher.Fetch(args[0].String(), headers)
	if err != nil {
		failed, _ := json.Marshal(reply{Error: err.Error()})
		return this.Context().NewString(string(failed)), nil
	}

	answered, err := json.Marshal(reply{
		Status:     out.Status,
		StatusText: out.StatusText,
		Headers:    out.Headers,
		Body:       base64.StdEncoding.EncodeToString(out.Body),
	})
	if err != nil {
		return nil, err
	}

	return this.Context().NewString(string(answered)), nil
}

// Extract turns a url (html empty, the parser fetches it) or a document (html
// given) into an article. format is one of text, markdown or html.
//
// An extraction that has started cannot be interrupted: QuickJS gives the host no
// way to stop a running script, so ctx only decides whether a request still wants
// to run once a runtime becomes free.
func (e *Engine) Extract(ctx context.Context, rawURL, html, format string) (*Article, error) {
	wait := time.NewTimer(e.QueueWait)
	defer wait.Stop()

	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-wait.C:
		return nil, ErrBusy
	}
	defer func() { <-e.sem }()

	rt, err := e.pool.Get()
	if err != nil {
		return nil, err
	}

	article, err := extract(rt, rawURL, html, format)

	// a runtime that trapped cannot be trusted with the next request, while a
	// rejected promise is the parser saying "no" and changes nothing
	var trapped *RuntimeError

	if errors.As(err, &trapped) {
		e.drop(rt)
	} else {
		e.pool.Put(rt)
	}

	return article, err
}

// rejectMessage strips what QuickJS hangs off a rejected promise: the stack of
// the bundle, which names internals and means nothing to whoever asked.
func rejectMessage(text string) string {
	first, _, _ := strings.Cut(text, "\n")

	return strings.TrimSpace(strings.TrimPrefix(first, "Error: "))
}

// ParserError is the parser declining a document: the url could not be fetched,
// it was not a document, or nothing in it looked like an article.
type ParserError struct{ Message string }

func (e *ParserError) Error() string { return e.Message }

// RuntimeError is QuickJS failing rather than the document being bad: out of
// memory, stack exhaustion, a trapped wasm call. Such a runtime is thrown away.
type RuntimeError struct{ error }

func extract(rt *qjs.Runtime, rawURL, html, format string) (article *Article, err error) {
	// QuickJS reports fatal problems (out of memory, stack exhaustion) as panics
	defer func() {
		if r := recover(); r != nil {
			err = &RuntimeError{fmt.Errorf("%v", r)}
		}
	}()

	result, err := rt.Context().Global().Invoke("extract", rawURL, html, format)
	if err != nil {
		return nil, &RuntimeError{err}
	}
	// the bundle hands back a native promise. Await drains QuickJS's job queue and
	// takes ownership of the promise: freeing result here corrupts the runtime.
	settled, err := result.Await()
	if err != nil {
		// a rejection is the parser refusing this document, the runtime is fine
		return nil, errors.New(rejectMessage(err.Error()))
	}
	defer settled.Free()

	// the parser declines a document it cannot turn into an article by answering
	// { error: true, message } instead of throwing
	var parsed struct {
		Article
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(settled.String()), &parsed); err != nil {
		return nil, fmt.Errorf("decode parser result: %w", err)
	}

	if parsed.Error {
		return nil, &ParserError{Message: parsed.Message}
	}

	return &parsed.Article, nil
}

// drop removes a broken runtime from the bookkeeping and frees it.
func (e *Engine) drop(rt *qjs.Runtime) {
	e.mu.Lock()

	for i, r := range e.runtimes {
		if r == rt {
			e.runtimes = append(e.runtimes[:i], e.runtimes[i+1:]...)
			break
		}
	}

	e.mu.Unlock()

	closeRuntime(rt)
}

// closeRuntime frees a runtime. A runtime that trapped on the way out can panic
// while being freed, which must not take the process down.
func closeRuntime(rt *qjs.Runtime) {
	defer func() { recover() }() //nolint:errcheck // nothing left to do if freeing fails

	rt.Close()
}

// Close frees all runtimes created by this engine.
func (e *Engine) Close() {
	e.mu.Lock()
	runtimes := e.runtimes
	e.runtimes = nil
	e.mu.Unlock()

	for _, rt := range runtimes {
		closeRuntime(rt)
	}
}
