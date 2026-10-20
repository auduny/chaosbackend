package main

import (
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Set by goreleaser via -ldflags "-X main.version=..."
var version = "dev"

//go:embed template.html
var defaultTemplate string

var tmpl *template.Template

// Upper bound for generated bodies (bytes=)
const maxBodyBytes = 1 << 30

// sleep waits for d, returning false early if the client goes away.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// chance returns true with pct percent probability (0 = never, 100 = always).
func chance(pct int) bool {
	return rand.Intn(100) < pct
}

// parseInt parses a non-negative integer query value, returning def when empty.
func parseInt(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return n, nil
}

func parseMillis(s string) (time.Duration, error) {
	n, err := parseInt(s, 0)
	return time.Duration(n) * time.Millisecond, err
}

// parseStatus parses an HTTP status code, returning def when empty.
func parseStatus(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	code, err := strconv.Atoi(s)
	// net/http panics on codes outside 100-999, and 1xx are not final responses
	if err != nil || code < 200 || code > 999 {
		return 0, fmt.Errorf("invalid status code %q (must be 200-999)", s)
	}
	return code, nil
}

// splitChance splits "value,pct" into its parts. pct defaults to 100.
func splitChance(s string) (string, int, error) {
	value, pctStr, _ := strings.Cut(s, ",")
	pct, err := parseInt(pctStr, 100)
	if err != nil || pct > 100 {
		return "", 0, fmt.Errorf("invalid frequency %q (must be 0-100)", pctStr)
	}
	return value, pct, nil
}

// parsePorts expands "8080,9000-9002" into individual ports.
func parsePorts(s string) ([]string, error) {
	var ports []string
	for _, part := range strings.Split(s, ",") {
		startStr, endStr, isRange := strings.Cut(part, "-")
		if !isRange {
			endStr = startStr
		}
		start, err1 := strconv.Atoi(startStr)
		end, err2 := strconv.Atoi(endStr)
		if err1 != nil || err2 != nil || start < 1 || end > 65535 || start > end {
			return nil, fmt.Errorf("invalid port or port range %q", part)
		}
		for p := start; p <= end; p++ {
			ports = append(ports, strconv.Itoa(p))
		}
	}
	return ports, nil
}

// respond writes a plain text response, omitting the body where HTTP forbids one.
func respond(w http.ResponseWriter, code int, msg string) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(code)
	if code != http.StatusNoContent && code != http.StatusNotModified {
		fmt.Fprintln(w, msg)
	}
}

func badRequest(w http.ResponseWriter, err error) {
	log.Println("Bad request:", err)
	respond(w, http.StatusBadRequest, err.Error())
}

// hijackAndClose drops the TCP connection without sending a response.
func hijackAndClose(w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		log.Printf("Hijacking failed: %v\n", err)
		http.Error(w, "Hijacking failed", http.StatusInternalServerError)
		return
	}
	conn.Close()
}

// pageHandler serves the documentation page.
func pageHandler(w http.ResponseWriter, r *http.Request) {
	if err := tmpl.Execute(w, "This is the default page."); err != nil {
		log.Println("Template error:", err)
	}
}

// plan is the set of faults to inject into one response, with dice already rolled.
type plan struct {
	status     int
	delay      time.Duration
	drip       time.Duration // sleep between each body byte
	reset      bool
	truncate   bool
	truncateAt int // body bytes sent before aborting, -1 = half
	bytes      int // generated body size, -1 = short status message
	echo       bool
	chunked    bool
}

// presets keep the original endpoints working as defaults for the query parameters.
var presets = map[string]url.Values{
	"/error": {"status": {"500"}},
	"/reset": {"reset": {""}},
}

// parsePlan turns query parameters into a plan. Every fault works on every path,
// and those taking ",pct" only fire with that percent chance.
func parsePlan(q url.Values) (plan, error) {
	p := plan{status: http.StatusOK, truncateAt: -1, bytes: -1}

	if s := q.Get("status"); s != "" {
		codeStr, pct, err := splitChance(s)
		if err != nil {
			return p, err
		}
		code, err := parseStatus(codeStr, http.StatusOK)
		if err != nil {
			return p, err
		}
		if chance(pct) {
			p.status = code
		}
	}

	// sleep=base[,span[,pct]]; "slow" is the old name used by /new
	s := q.Get("sleep")
	if s == "" {
		s = q.Get("slow")
	}
	if s != "" {
		parts := strings.Split(s, ",")
		if len(parts) > 3 {
			return p, fmt.Errorf("invalid sleep %q (want base,span,frequency)", s)
		}
		parts = append(parts, "", "", "")
		base, err1 := parseInt(parts[0], 0)
		span, err2 := parseInt(parts[1], 0)
		pct, err3 := parseInt(parts[2], 100)
		if err := errors.Join(err1, err2, err3); err != nil || pct > 100 {
			return p, fmt.Errorf("invalid sleep %q (want base,span,frequency)", s)
		}
		delay := base
		if span > 0 && chance(pct) {
			delay += rand.Intn(span)
		}
		p.delay = time.Duration(delay) * time.Millisecond
	}

	var err error
	if p.drip, err = parseMillis(q.Get("sleepBetweenBytes")); err != nil {
		return p, err
	}

	if q.Has("reset") {
		_, pct, err := splitChance(q.Get("reset"))
		if err != nil {
			return p, err
		}
		p.reset = chance(pct)
	}

	if q.Has("truncate") {
		at, pct, err := splitChance(q.Get("truncate"))
		if err != nil {
			return p, err
		}
		if p.truncateAt, err = parseInt(at, -1); err != nil {
			return p, err
		}
		p.truncate = chance(pct)
	}

	if q.Has("bytes") {
		if p.bytes, err = parseInt(q.Get("bytes"), 1024); err != nil || p.bytes > maxBodyBytes {
			return p, fmt.Errorf("invalid bytes %q (must be 0-%d)", q.Get("bytes"), maxBodyBytes)
		}
	}
	p.echo = q.Has("echo")
	p.chunked = q.Has("chunked")
	return p, nil
}

// filler is an endless reader of a repeating pattern, for generated bodies.
type filler struct{ off int }

const fillPattern = "chaosbackend\n"

func (f *filler) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = fillPattern[(f.off+i)%len(fillPattern)]
	}
	f.off += len(b)
	return len(b), nil
}

// writeBody copies body to w, flushing and sleeping after each byte when drip > 0.
func writeBody(ctx context.Context, w http.ResponseWriter, body io.Reader, drip time.Duration) error {
	if drip <= 0 {
		_, err := io.Copy(w, body)
		return err
	}
	rc := http.NewResponseController(w)
	b := make([]byte, 1)
	for {
		if _, err := io.ReadFull(body, b); err != nil {
			return nil // body done
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if err := rc.Flush(); err != nil {
			return err
		}
		if !sleep(ctx, drip) {
			return ctx.Err()
		}
	}
}

// chaosHandler serves every path, injecting the faults given as query parameters.
// They are applied in order: sleep, reset, status and body, sleepBetweenBytes, truncate.
func chaosHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if r.URL.Path == "/" && len(q) == 0 {
		pageHandler(w, r)
		return
	}
	for k, v := range presets[r.URL.Path] {
		if !q.Has(k) {
			q[k] = v
		}
	}
	p, err := parsePlan(q)
	if err != nil {
		badRequest(w, err)
		return
	}
	log.Printf("Plan: status=%d sleep=%v sleepBetweenBytes=%v reset=%v truncate=%v", p.status, p.delay, p.drip, p.reset, p.truncate)

	if !sleep(r.Context(), p.delay) {
		return
	}
	if p.reset {
		hijackAndClose(w)
		return
	}

	var body io.Reader
	var size int
	switch {
	case p.echo:
		dump, err := httputil.DumpRequest(r, true)
		if err != nil {
			respond(w, http.StatusInternalServerError, err.Error())
			return
		}
		dump = append([]byte("Remote: "+r.RemoteAddr+"\n\n"), dump...)
		body, size = bytes.NewReader(dump), len(dump)
	case p.bytes >= 0:
		body, size = io.LimitReader(&filler{}, int64(p.bytes)), p.bytes
	default:
		msg := fmt.Sprintf("Returning Statuscode: %d in %dms\n", p.status, p.delay.Milliseconds())
		body, size = strings.NewReader(msg), len(msg)
	}

	h := w.Header()
	if h.Get("Content-Type") == "" {
		if p.drip > 0 {
			h.Set("Content-Type", "text/event-stream")
		} else {
			h.Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	if p.status == http.StatusNoContent || p.status == http.StatusNotModified {
		w.WriteHeader(p.status) // no body allowed
		return
	}
	if !p.chunked {
		h.Set("Content-Length", strconv.Itoa(size))
	}
	w.WriteHeader(p.status)

	if !p.truncate {
		writeBody(r.Context(), w, body, p.drip)
		return
	}
	cut := p.truncateAt
	if cut < 0 || cut >= size {
		cut = size / 2
	}
	if writeBody(r.Context(), w, io.LimitReader(body, int64(cut)), p.drip) != nil {
		return
	}
	http.NewResponseController(w).Flush()
	log.Println("Truncating response after", cut, "of", size, "bytes")
	panic(http.ErrAbortHandler) // makes net/http close the connection without logging a stack trace
}

// healthHandler serves a health check whose state can be flipped per listener,
// e.g. to take a single backend out of a load balancer.
func healthHandler(healthy *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health/up":
			healthy.Store(true)
		case "/health/down":
			healthy.Store(false)
		case "/health/toggle":
			healthy.Store(!healthy.Load())
		}
		if healthy.Load() {
			respond(w, http.StatusOK, "OK")
		} else {
			respond(w, http.StatusServiceUnavailable, "DOWN")
		}
	}
}

// statusRecorder captures the status code for logging. Unwrap lets
// http.ResponseController reach the underlying Flusher/Hijacker.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// middleware tags responses with the serving listener, applies header=Name:Value
// query parameters and logs each request.
func middleware(backend string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			status := "-" // no response written: hijacked or client gone
			if rec.status != 0 {
				status = strconv.Itoa(rec.status)
			}
			log.Printf("%s %s %s %s %s %s", backend, r.RemoteAddr, r.Method, r.URL.RequestURI(), status, time.Since(start).Round(time.Millisecond))
		}()

		w.Header().Set("X-Backend", backend)
		for _, h := range r.URL.Query()["header"] {
			name, value, ok := strings.Cut(h, ":")
			if !ok || name == "" {
				badRequest(rec, fmt.Errorf("invalid header %q (want Name:Value)", h))
				return
			}
			w.Header().Add(name, value)
		}
		next.ServeHTTP(rec, r)
	})
}

func newMux(backend string) http.Handler {
	healthy := &atomic.Bool{}
	healthy.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(healthy))
	mux.HandleFunc("/health/", healthHandler(healthy))
	mux.HandleFunc("/", chaosHandler)
	return middleware(backend, mux)
}

func main() {
	var (
		addressesInput string
		portsInput     string
		templateFile   string
		certFile       string
		keyFile        string
		showVersion    bool
	)
	flag.StringVar(&addressesInput, "a", "127.0.0.1", "Comma-separated list of addresses")
	flag.StringVar(&portsInput, "p", "8080", "Comma-separated list of ports or port ranges (e.g., 4000-4020)")
	flag.StringVar(&templateFile, "template", "", "Path to HTML template file for the default page (default: built-in)")
	flag.StringVar(&certFile, "cert", "", "TLS certificate file (enables HTTPS together with -key)")
	flag.StringVar(&keyFile, "key", "", "TLS private key file")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("chaosbackend", version)
		return
	}
	if (certFile == "") != (keyFile == "") {
		log.Fatal("-cert and -key must be used together")
	}

	var err error
	if templateFile != "" {
		tmpl, err = template.ParseFiles(templateFile)
	} else {
		tmpl, err = template.New("default").Parse(defaultTemplate)
	}
	if err != nil {
		log.Fatal("Parsing template: ", err)
	}

	ports, err := parsePorts(portsInput)
	if err != nil {
		log.Fatal(err)
	}

	// Cancelled on Ctrl+C / SIGTERM, which also aborts any in-flight sleeps
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bind everything up front so a busy port fails fast
	var servers []*http.Server
	var listeners []net.Listener
	for _, address := range strings.Split(addressesInput, ",") {
		for _, port := range ports {
			addr := net.JoinHostPort(address, port)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				log.Fatal(err)
			}
			servers = append(servers, &http.Server{
				Handler:           newMux(addr),
				ReadHeaderTimeout: 10 * time.Second,
				BaseContext:       func(net.Listener) context.Context { return ctx },
				// Stick to HTTP/1.1 over TLS, since connection hijacking (/reset) is not possible with HTTP/2
				TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
			})
			listeners = append(listeners, ln)
		}
	}

	scheme := "http"
	if certFile != "" {
		scheme = "https"
	}
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(srv *http.Server, ln net.Listener) {
			defer wg.Done()
			log.Printf("Starting server on %s://%s", scheme, ln.Addr())
			var err error
			if certFile != "" {
				err = srv.ServeTLS(ln, certFile, keyFile)
			} else {
				err = srv.Serve(ln)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}(srv, listeners[i])
	}
	log.Println("Number of servers:", len(servers))

	<-ctx.Done()
	log.Println("Shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range servers {
		srv.Shutdown(shutdownCtx)
	}
	wg.Wait()
}
