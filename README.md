# HumanTXT API

Get the human readable text of a website, plus the metadata around it, from one HTTP call.

The extraction itself is [Postlight Parser](https://github.com/postlight/parser) (Mercury). It used to
run as a Node CLI per request; now the parser is bundled once and executed inside **QuickJS compiled to
WebAssembly** ([fastschema/qjs](https://github.com/fastschema/qjs)), in the same process as the HTTP
server. One static binary, no Node, no child processes.

```bash
curl 'https://humantxt.acona.app/?url=https://orf.at'
```

## API

### By URL

```
GET /?url=<url>&format=<text|markdown|html>
```

`format` defaults to `text`. A bare domain (`orf.at`) is fetched over https.

### By posted document

Send the HTML instead of letting the service fetch it. Useful behind a login, for a rendered DOM, or for
a page that is already somewhere else:

```bash
curl -X POST --data-binary @page.html 'http://localhost:8080/?format=markdown'
curl -X POST -H 'Content-Type: application/json' \
     -d '{"html":"<html>...","url":"https://example.com/story","format":"text"}' \
     'http://localhost:8080/'
```

A body posted as `application/json` may carry `html`, `url` and `format`; anything else is taken as the
document itself. `url` is optional in both cases — it selects the parser's per-site rules and makes
relative links absolute, so pass it whenever you know it. A posted body must be UTF-8 and at most
**512 KiB**, which is also the largest page the service will download (`fetch.MaxDocument`).

### Response

```json
{
  "title": "Thunder (mascot)",
  "author": "Wikipedia Contributors",
  "date_published": "2016-09-16T20:56:00.000Z",
  "dek": null,
  "lead_image_url": null,
  "content": "... **Thunder** is the [stage name](https://en.wikipedia.org/wiki/Stage_name) ...",
  "next_page_url": null,
  "url": "https://en.wikipedia.org/wiki/Thunder_(mascot)",
  "domain": "en.wikipedia.org",
  "excerpt": "Thunder is the stage name for the horse ...",
  "word_count": 4677,
  "direction": "ltr",
  "total_pages": 1,
  "rendered_pages": 1
}
```

Wrong or missing parameters answer `400` with the message the old service used (`Parameter 'url' not
found`, `Invalid url: '...'`, `Invalid format: '...'`), `405` for verbs other than GET/POST and `413` for
an oversized posted body. A document the parser refuses to work on is a `400` whose text says why, and a
page that could not be downloaded or that the parser declines is answered `200` with the parser's own
error object, exactly as the CLI printed it before:

```json
{
  "error": true,
  "message": "Resource returned a response status code of 404"
}
```

## How it is put together

| | |
|---|---|
| `cmd/humantxt` | HTTP server, request logging, graceful shutdown |
| `internal/api` | request parsing (query string, HTML body, JSON body), format and URL validation |
| `internal/vm` | QuickJS runtime pool, the parser's JSON in and out |
| `internal/fetch` | the only thing in the process that opens connections |
| `js/` | source of the bundle the binary embeds (needs node to rebuild) |
| `internal/vm/bundle/humantxt.js` | the generated bundle, committed so `go build` needs no node |

The parser is not passed a URL to fetch and forget about: it asks for URLs itself (the one you gave it
and page 2 and beyond of a paginated article) through a `postman-request` shim that calls back into Go,
where the timeout, the document size limit and the guard against reaching into private networks live.

## Limits

Parsing is the expensive part and its cost grows faster than linearly with the size of a page, so the
service bounds the work rather than the patience of its clients:

| | |
|---|---|
| document size | 512 KiB, posted or downloaded. `413` for a body, a parser error for a download |
| concurrent extractions | `min(NumCPU, 8)` QuickJS runtimes |
| waiting for a free runtime | 15 s, then `503` (`vm.Engine.QueueWait`) |
| one download | 15 s including its body, 10 redirects |
| QuickJS heap | 192 MiB per runtime, past which the document is refused and the runtime replaced |

Requests are only abandoned while they wait: QuickJS gives the host no way to interrupt a script that has
started, so a client that hangs up mid-extraction does not stop the work, it only stops the answer from
going anywhere.

```bash
make build test vet     # go
make bundle             # regenerate internal/vm/bundle/humantxt.js
make goldens            # regenerate the expected parser output the tests compare against
```

`internal/vm/testdata/golden` holds output produced by Postlight Parser under Node. The Go tests fail as
soon as the bundle stops behaving like the Node library, which is the whole point of running it this way.

## Differences from the old CLI based service

- Multi page articles still work, but each page is fetched by Go instead of by Node's HTTP client.
- Requests send the user agent Postlight Parser sends (`Mozilla/5.0 (Windows NT 6.1) ... Chrome/41`), not
  one of this service. Same as the old deployment.
- Pages are downloaded by Go rather than by Node's HTTP client, and the parser's own checks on the
  response (status code, content type, content length) still run.
- Downloads keep no cookie jar. A multi page article that needs the cookie set by page one will not get
  past page one.
- The guard refuses hosts that resolve only to private, link-local or carrier-grade ranges (the latter is
  what mesh networks like Tailscale hand out), and it dials ports 80 and 443 only, so the service cannot
  be pointed at a database on some other port - or used to look for one.
- QuickJS interprets; it has no JIT. Measured on one machine, parsing text out of:

  | document | here | same document under node |
  |---|---|---|
  | 100 KB | 2.7 s | 0.1 s |
  | 250 KB | 8.9 s | 0.14 s |
  | 500 KB | 25 s | 0.32 s |

  (`js/perf.mjs` measures the node column, the size benchmarks in `internal/vm` measure this one.) A
  normal news article is 1-2 s. Concurrency is capped at the runtime pool so slow pages queue instead of
  multiplying, and the queue is bounded (`503`).

## Run it

```bash
make run                        # listens on :8080, override with ADDR=:9000 ./humantxt
docker build -t humantxt .
docker compose up --build       # exposes 100.127.213.6:19302
```

`HUMANTXT_ALLOW_PRIVATE=1` lifts the whole guard: private and link-local addresses and ports other than
80 and 443. Only do that where the API itself is not reachable from the internet.
