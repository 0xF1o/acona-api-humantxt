// Package fetch downloads pages for the extraction bundle. The parser asks for
// URLs itself (the url of a request and page 2 and beyond of an article), so
// every byte that leaves the process goes through here: timeouts, size limits
// and the check that we do not reach into private networks.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const defaultUserAgent = "Mozilla/5.0 (compatible; HumanTXT/2.0; +https://humantxt.acona.app)"

// MaxDocument is the largest document the service turns into an article. The
// parser's cost grows faster than linearly with the size of a page - a 1 MB
// document costs roughly a minute of cpu - so a request without this cap could
// hold a runtime for minutes. Both posted bodies and downloaded pages are capped
// here.
const MaxDocument = 512 << 10

// webPorts are the ports the guard lets the downloader connect to. A service that
// fetches addresses handed to it by strangers must not turn into a port scanner,
// so anything else is refused unless the guard is lifted.
var webPorts = []string{"80", "443"}

var allowedPorts = func() map[string]bool {
	ports := make(map[string]bool, len(webPorts))
	for _, port := range webPorts {
		ports[port] = true
	}

	return ports
}()

// notPublic are ranges that a plain privacy test lets through: carrier grade NAT,
// which is what mesh networks such as Tailscale hand out, and the benchmarking
// range.
var notPublic = prefixes("100.64.0.0/10", "198.18.0.0/15")

func prefixes(nets ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(nets))

	for _, cidr := range nets {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			panic("fetch: bad blocked prefix " + cidr)
		}

		out = append(out, prefix)
	}

	return out
}

// ErrBlockedAddress is returned when a host only resolves to addresses that are
// not reachable from the public internet (SSRF guard).
var ErrBlockedAddress = errors.New("address is not publicly reachable")

// Reply is what the parser needs to know about a response.
type Reply struct {
	Status     int
	StatusText string
	Headers    map[string]string
	Body       []byte
}

type Options struct {
	// Timeout bounds a single request, including its body.
	Timeout time.Duration
	// MaxBytes is the largest accepted response body, MaxDocument by default.
	MaxBytes int64
	// UserAgent is sent when the caller does not name one. Defaults to a
	// generic browser-ish agent.
	UserAgent string
	// AllowPrivate disables the SSRF guard, for parsing hosts of your own.
	AllowPrivate bool
}

// Fetcher downloads documents without interpreting them; charset conversion is
// done by the parser.
type Fetcher struct {
	client    *http.Client
	userAgent string
	maxBytes  int64
}

// New returns a Fetcher; zero Options fields use sensible defaults.
func New(o Options) *Fetcher {
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}

	if o.MaxBytes == 0 {
		o.MaxBytes = MaxDocument
	}

	if o.UserAgent == "" {
		o.UserAgent = defaultUserAgent
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dial(ctx, dialer, addr, o.AllowPrivate)
		},
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        32,
	}

	return &Fetcher{
		userAgent: o.UserAgent,
		maxBytes:  o.MaxBytes,
		client: &http.Client{
			Transport: transport,
			Timeout:   o.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
				}
				return nil
			},
		},
	}
}

// Fetch retrieves rawURL and returns the response as the parser expects it.
// Headers passed in take precedence over the defaults.
//
// shortcut: the request runs on its own context, so a client that hangs up does
// not cancel a download in flight; client.Timeout bounds it instead.
func (f *Fetcher) Fetch(rawURL string, headers map[string]string) (*Reply, error) {
	u, err := ParseURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")

	for name, value := range headers {
		if name == "" {
			continue
		}
		req.Header.Set(name, value)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", u, unwrapURLError(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", u, err)
	}

	if int64(len(body)) > f.maxBytes {
		return nil, fmt.Errorf("%s is larger than the %d byte document limit", u, f.maxBytes)
	}

	return &Reply{
		Status: resp.StatusCode,
		// the parser compares statusMessage against 'OK', so no "200 OK" here
		StatusText: statusText(resp),
		Headers:    replyHeaders(resp.Header),
		Body:       body,
	}, nil
}

// statusText gives the parser the bare reason phrase: it compares statusMessage
// against 'OK'.
func statusText(resp *http.Response) string {
	if text := http.StatusText(resp.StatusCode); text != "" {
		return text
	}

	return resp.Status
}

// replyHeaders keeps the headers the parser looks at, in lowercase like a Node
// client hands them out: content type and length are validated, set-cookie kept
// so a session survives the pages of an article.
func replyHeaders(h http.Header) map[string]string {
	out := make(map[string]string, 3)

	for _, name := range []string{"content-type", "content-length", "set-cookie"} {
		if value := h.Get(name); value != "" {
			out[name] = value
		}
	}

	return out
}

// ParseURL validates a user supplied address, assuming https for bare domains.
func ParseURL(rawURL string) (*url.URL, error) {
	if rawURL == "" {
		return nil, errors.New("url is empty")
	}

	candidate := rawURL
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}

	u, err := url.Parse(candidate)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", rawURL, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid url %q: unsupported scheme %q", rawURL, u.Scheme)
	}

	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid url %q: no host", rawURL)
	}

	return u, nil
}

// dial resolves the host and dials a validated IP, so the address that was
// checked is the address that is connected to. This is where the guard lives, and
// it is applied to every redirect hop because each hop dials again.
func dial(ctx context.Context, dialer *net.Dialer, addr string, allowPrivate bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}

	// a host that resolves to both a public and a private address is dialed at its
	// public address only, so the guard cannot be walked around with a second
	// record or with the order the resolver returns them in
	targets := make([]string, 0, len(ips))

	for _, ip := range ips {
		if allowPrivate || isPublic(ip) {
			targets = append(targets, ip)
		}
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, host)
	}

	if !allowPrivate && !allowedPort(port) {
		return nil, fmt.Errorf("%s port %s is not a web port (%s)", host, port, strings.Join(webPorts, ", "))
	}

	lastErr := fmt.Errorf("no addresses for %s", host)

	for _, ip := range targets {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
		if err != nil {
			lastErr = err
			continue
		}

		return conn, nil
	}

	return nil, lastErr
}

// allowedPort refuses ports other than the web ports, which a redirect could
// otherwise move the connection to after the url was validated.
func allowedPort(port string) bool {
	return port == "" || allowedPorts[port]
}

// isPublic reports whether an address is a normal public one.
func isPublic(address string) bool {
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}

	// a zone marks a link scoped address, which is never a public target
	addr = addr.Unmap()
	if addr.Zone() != "" || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}

	for _, blocked := range notPublic {
		if blocked.Contains(addr) {
			return false
		}
	}

	return true
}

func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}

	return err
}
