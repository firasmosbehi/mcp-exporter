package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/firasmosbehi/mcp-exporter/internal/observe"
)

// maxObserved caps how much of a single body or SSE event we buffer for
// parsing. Bigger frames are still forwarded, just not observed.
const maxObserved = 16 << 20

const sessionHeader = "Mcp-Session-Id"

type exchangeKey struct{}

type exchange struct {
	ctx     context.Context // carries the transport-level trace context
	scope   string
	session string
}

// HTTP returns a Streamable HTTP reverse proxy to upstream (a base URL;
// request paths pass through unchanged), plus /healthz and /readyz.
func HTTP(t *observe.Tracker, upstream *url.URL) http.Handler {
	h, _ := handler(t, upstream)
	return h
}

// Route is one upstream of a gateway, served under /Name/.
type Route struct {
	Name     string
	Tracker  *observe.Tracker
	Upstream *url.URL
}

// Gateway proxies /NAME/... to each route's upstream with the prefix
// stripped, so a route's own /healthz and /readyz are /NAME/healthz and
// /NAME/readyz. Each route has its own Tracker, so sessions and request ids
// never mix across upstreams. /readyz is ready when every route is.
func Gateway(routes []Route) http.Handler {
	mux := http.NewServeMux()
	var rds []*readiness
	for _, r := range routes {
		h, rd := handler(r.Tracker, r.Upstream)
		rds = append(rds, rd)
		h = http.StripPrefix("/"+r.Name, h)
		mux.Handle("/"+r.Name, h)
		mux.Handle("/"+r.Name+"/", h)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		for i, rd := range rds {
			if err := rd.check(); err != nil {
				http.Error(w, routes[i].Name+": upstream unreachable: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		io.WriteString(w, "ok\n")
	})
	return mux
}

func handler(t *observe.Tracker, upstream *url.URL) (http.Handler, *readiness) {
	var seq atomic.Uint64
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.SetXForwarded()
			// Compressed responses can't be observed, and the sidecar hop is
			// local, so ask the upstream for plain bytes (also keeps SSE flushing).
			r.Out.Header.Set("Accept-Encoding", "identity")
		},
		FlushInterval: -1, // SSE must not be buffered
		ModifyResponse: func(resp *http.Response) error {
			ex := resp.Request.Context().Value(exchangeKey{}).(*exchange)
			sess := ex.session
			if s := resp.Header.Get(sessionHeader); s != "" {
				sess = s
			}
			if resp.Request.Method == http.MethodDelete && resp.StatusCode < 300 {
				t.EndSession(ex.session)
			}
			frame := func(b []byte) { t.Frame(ex.ctx, observe.ServerToClient, ex.scope, sess, b) }
			switch ct := resp.Header.Get("Content-Type"); {
			case strings.HasPrefix(ct, "text/event-stream"):
				resp.Body = &sseObserver{ReadCloser: resp.Body, frame: frame}
			case strings.HasPrefix(ct, "application/json"):
				resp.Body = &jsonObserver{ReadCloser: resp.Body, frame: frame}
			}
			return nil
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	rd := &readiness{addr: hostPort(upstream)}
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if err := rd.check(); err != nil {
			http.Error(w, "upstream unreachable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ex := &exchange{
			ctx:     otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header)),
			session: r.Header.Get(sessionHeader),
		}
		// Requests share an id space only within a session; before one exists
		// (initialize) each HTTP exchange is its own scope.
		ex.scope = ex.session
		if ex.scope == "" {
			ex.scope = "x" + strconv.FormatUint(seq.Add(1), 36)
		}
		if r.Method == http.MethodPost && r.ContentLength <= maxObserved {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxObserved+1))
			r.Body.Close()
			if err != nil {
				http.Error(w, "reading request body: "+err.Error(), http.StatusBadRequest)
				return
			}
			enc := r.Header.Get("Content-Encoding")
			if enc == "gzip" {
				if plain, err := gunzip(body); err == nil {
					body, enc = plain, ""
					r.Header.Del("Content-Encoding")
				}
			}
			// Other encodings (or bad gzip) are forwarded as-is, unobserved.
			if enc == "" && len(body) <= maxObserved {
				body = t.Frame(ex.ctx, observe.ClientToServer, ex.scope, ex.session, body)
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			r.Header.Set("Content-Length", strconv.Itoa(len(body)))
		}
		rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), exchangeKey{}, ex)))
	})
	return mux, rd
}

// readyTTL is how long a readiness result is reused, so frequent probes
// don't turn into a dial per probe.
var readyTTL = 2 * time.Second

// readiness reports whether the upstream accepts TCP connections.
type readiness struct {
	addr string
	mu   sync.Mutex
	at   time.Time
	err  error
}

func (r *readiness) check() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.at.IsZero() && time.Since(r.at) < readyTTL {
		return r.err
	}
	c, err := net.DialTimeout("tcp", r.addr, time.Second)
	if err == nil {
		c.Close()
	}
	r.at, r.err = time.Now(), err
	return err
}

func hostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return net.JoinHostPort(u.Hostname(), "443")
	}
	return net.JoinHostPort(u.Hostname(), "80")
}

var errTooBig = errors.New("decompressed body exceeds observation limit")

// gunzip decompresses at most maxObserved bytes, so a small gzip bomb can't
// make us allocate without bound.
func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(zr, maxObserved+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxObserved {
		return nil, errTooBig
	}
	return out, nil
}

// jsonObserver buffers a JSON response as it streams to the client and
// observes it once complete.
type jsonObserver struct {
	io.ReadCloser
	frame func([]byte)
	buf   []byte
	done  bool
}

func (o *jsonObserver) Read(p []byte) (int, error) {
	n, err := o.ReadCloser.Read(p)
	if len(o.buf)+n <= maxObserved {
		o.buf = append(o.buf, p[:n]...)
	} else {
		o.done = true // too big: forward without observing
	}
	if err == io.EOF && !o.done {
		o.done = true
		if len(bytes.TrimSpace(o.buf)) > 0 {
			o.frame(o.buf)
		}
	}
	return n, err
}

// sseObserver parses Server-Sent Events as they stream past and observes the
// data of each event. The bytes forwarded are never modified.
type sseObserver struct {
	io.ReadCloser
	frame func([]byte)
	line  []byte
	data  []byte
}

func (o *sseObserver) Read(p []byte) (int, error) {
	n, err := o.ReadCloser.Read(p)
	for _, c := range p[:n] {
		if c != '\n' {
			if len(o.line) < maxObserved {
				o.line = append(o.line, c)
			}
			continue
		}
		l := bytes.TrimSuffix(o.line, []byte{'\r'})
		switch {
		case len(l) == 0: // blank line dispatches the event
			if len(o.data) > 0 {
				o.frame(o.data)
			}
			o.data = o.data[:0]
		case bytes.HasPrefix(l, []byte("data:")):
			d := bytes.TrimPrefix(bytes.TrimPrefix(l, []byte("data:")), []byte(" "))
			if len(o.data) > 0 {
				o.data = append(o.data, '\n')
			}
			o.data = append(o.data, d...)
		}
		o.line = o.line[:0]
	}
	return n, err
}
