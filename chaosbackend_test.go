package main

import (
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	tmpl = template.Must(template.New("default").Parse(defaultTemplate))
	srv := httptest.NewServer(newMux("test-backend"))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string, error) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp, string(body), err
}

func TestParsePorts(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"8080", []string{"8080"}, false},
		{"8080,9000-9002", []string{"8080", "9000", "9001", "9002"}, false},
		{"0", nil, true},
		{"65536", nil, true},
		{"9002-9000", nil, true},
		{"9000-", nil, true},
		{"abc", nil, true},
		{"", nil, true},
	}
	for _, tt := range tests {
		got, err := parsePorts(tt.in)
		if (err != nil) != tt.wantErr || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parsePorts(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestParseStatus(t *testing.T) {
	for _, s := range []string{"abc", "0", "99", "100", "1000", "-1"} {
		if _, err := parseStatus(s, 500); err == nil {
			t.Errorf("parseStatus(%q) should fail", s)
		}
	}
	if code, _ := parseStatus("", 500); code != 500 {
		t.Errorf("empty status should give default, got %d", code)
	}
	if code, _ := parseStatus("503", 500); code != 503 {
		t.Errorf("want 503, got %d", code)
	}
}

func TestChanceBounds(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if chance(0) {
			t.Fatal("chance(0) returned true")
		}
		if !chance(100) {
			t.Fatal("chance(100) returned false")
		}
	}
}

func TestStatusCodes(t *testing.T) {
	srv := newTestServer(t)
	tests := []struct {
		path string
		want int
	}{
		{"/", 200},
		{"/any/other/path", 200},
		{"/error", 500},
		{"/error?status=404", 404},
		{"/error?status=204", 204},
		{"/error?status=5000", 400},
		{"/error?status=abc", 400},
		{"/error?sleep=-1", 400},
		{"/new", 200},
		{"/foo?status=503", 503},
		{"/foo?status=503,100", 503},
		{"/foo?status=503,0", 200},
		{"/foo?status=abc", 400},
		{"/foo?status=503,101", 400},
		{"/new?slow=10,0", 200},
		{"/new?slow=10,5,50", 200},
		{"/foo?sleep=10,5,50", 200},
		{"/new?slow=a,b", 400},
		{"/foo?reset=1,0", 200},
		{"/reset?reset=1,0", 200},
		{"/health", 200},
		{"/foo?bytes=-1", 400},
		{"/foo?truncate=x", 400},
		{"/error?header=bogus", 400},
	}
	for _, tt := range tests {
		resp, _, err := get(t, srv, tt.path)
		if err != nil {
			t.Errorf("%s: %v", tt.path, err)
			continue
		}
		if resp.StatusCode != tt.want {
			t.Errorf("%s: got %d, want %d", tt.path, resp.StatusCode, tt.want)
		}
	}
}

func TestSleepDelays(t *testing.T) {
	srv := newTestServer(t)
	start := time.Now()
	if _, _, err := get(t, srv, "/error?status=404&sleep=200"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Errorf("response came after %v, want >= 200ms", d)
	}
}

func TestResetClosesConnection(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/reset", "/new?reset=1", "/foo?reset", "/foo?status=503&sleep=50&reset=1,100"} {
		if _, _, err := get(t, srv, path); err == nil {
			t.Errorf("%s: expected connection error", path)
		}
	}
}

func TestSlowStreams(t *testing.T) {
	srv := newTestServer(t)
	resp, body, err := get(t, srv, "/slow?sleepBetweenBytes=1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(body, "Returning Statuscode: 200") {
		t.Errorf("unexpected response: %v %q", resp.Header, body)
	}
}

func TestBytes(t *testing.T) {
	srv := newTestServer(t)
	for _, n := range []int{0, 1, 1024, 100000} {
		resp, body, err := get(t, srv, "/foo?bytes="+strconv.Itoa(n))
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != n || resp.ContentLength != int64(n) {
			t.Errorf("bytes=%d: got body %d, Content-Length %d", n, len(body), resp.ContentLength)
		}
	}
	resp, body, err := get(t, srv, "/foo?bytes=5000&chunked")
	if err != nil || len(body) != 5000 || resp.ContentLength != -1 {
		t.Errorf("chunked: err=%v body=%d Content-Length=%d", err, len(body), resp.ContentLength)
	}
}

func TestTruncate(t *testing.T) {
	srv := newTestServer(t)
	tests := []struct {
		path string
		want int
	}{
		{"/foo?bytes=1000&truncate", 500},
		{"/foo?bytes=1000&truncate=100", 100},
		{"/foo?bytes=1000&truncate=100,100", 100},
		{"/foo?bytes=1000&truncate=0&status=503", 0},
		{"/foo?bytes=1000&truncate=10&sleepBetweenBytes=1", 10},
		{"/foo?bytes=1000&truncate=10&chunked", 10},
	}
	for _, tt := range tests {
		_, body, err := get(t, srv, tt.path)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%s: want unexpected EOF, got %v", tt.path, err)
		}
		if len(body) != tt.want {
			t.Errorf("%s: got %d bytes before the cut, want %d", tt.path, len(body), tt.want)
		}
	}
	if _, body, err := get(t, srv, "/foo?bytes=1000&truncate=10,0"); err != nil || len(body) != 1000 {
		t.Errorf("truncate with 0%% chance should give full body, got %d, %v", len(body), err)
	}
}

func TestCombined(t *testing.T) {
	srv := newTestServer(t)
	start := time.Now()
	resp, body, err := get(t, srv, "/some/page?status=503&sleep=100&bytes=300&header=Cache-Control:max-age=5")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 || len(body) != 300 || resp.Header.Get("Cache-Control") != "max-age=5" || time.Since(start) < 100*time.Millisecond {
		t.Errorf("got %d, %d bytes, %v, after %v", resp.StatusCode, len(body), resp.Header, time.Since(start))
	}
}

func TestHealthToggle(t *testing.T) {
	srv := newTestServer(t)
	steps := []struct {
		path string
		want int
	}{
		{"/health", 200},
		{"/health/down", 503},
		{"/health", 503},
		{"/health/toggle", 200},
		{"/health/toggle", 503},
		{"/health/up", 200},
	}
	for _, s := range steps {
		resp, _, err := get(t, srv, s.path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != s.want {
			t.Errorf("%s: got %d, want %d", s.path, resp.StatusCode, s.want)
		}
	}
}

func TestHeadersAndEcho(t *testing.T) {
	srv := newTestServer(t)
	resp, body, err := get(t, srv, "/whatever?echo&header=Cache-Control:max-age=60&header=X-Foo:a:b")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Cache-Control"); got != "max-age=60" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := resp.Header.Get("X-Foo"); got != "a:b" {
		t.Errorf("X-Foo = %q", got)
	}
	if got := resp.Header.Get("X-Backend"); got != "test-backend" {
		t.Errorf("X-Backend = %q", got)
	}
	if !strings.Contains(body, "GET /whatever?echo") || !strings.Contains(body, "User-Agent:") {
		t.Errorf("echo body missing request: %q", body)
	}
}
