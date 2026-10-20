package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type connection struct {
	statusCode     int
	statusCodeFreq int
	slowc          int
	slowcSpan      int
	slowcFreq      int
}

//go:embed template.html
var defaultTemplate string

var tmpl *template.Template

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

// chance returns true with pct percent probability (0 = never, 100 = always).
func chance(pct int) bool {
	return rand.Intn(100) < pct
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

// respond writes a plain text response, omitting the body where HTTP forbids one.
func respond(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
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

func defaultHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Backend", "default")
	if err := tmpl.Execute(w, "This is the default page."); err != nil {
		log.Println("Template error:", err)
	}
}

func slowHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sleepBeforeFirstByte, err := parseMillis(q.Get("sleep"))
	if err != nil {
		badRequest(w, err)
		return
	}
	sleepBetweenBytes, err := parseMillis(q.Get("sleepBetweenBytes"))
	if err != nil {
		badRequest(w, err)
		return
	}
	content := "Example content delivered slowly. Connect:" + sleepBeforeFirstByte.String() + " Betweenbytes:" + sleepBetweenBytes.String()
	log.Println(content)
	if !sleep(r.Context(), sleepBeforeFirstByte) {
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range content {
		fmt.Fprintf(w, "%c", c)
		if err := rc.Flush(); err != nil {
			return
		}
		if !sleep(r.Context(), sleepBetweenBytes) {
			return
		}
	}
}

func errorHandler(w http.ResponseWriter, r *http.Request) {
	statusCode, err := parseStatus(r.URL.Query().Get("status"), http.StatusInternalServerError)
	if err != nil {
		badRequest(w, err)
		return
	}
	sleepBeforeFirstByte, err := parseMillis(r.URL.Query().Get("sleep"))
	if err != nil {
		badRequest(w, err)
		return
	}
	log.Println("Returning Statuscode:", statusCode, "Sleeping for", sleepBeforeFirstByte)
	if !sleep(r.Context(), sleepBeforeFirstByte) {
		return
	}
	respond(w, statusCode, "Returning Statuscode: "+strconv.Itoa(statusCode))
}

func resetConnectionHandler(w http.ResponseWriter, r *http.Request) {
	hijackAndClose(w)
}

func addHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backends", "snuskepus")

		next.ServeHTTP(w, r)
	})
}

func newHandler(w http.ResponseWriter, r *http.Request) {
	conn := connection{statusCode: 200, slowcFreq: 10}
	if r.URL.Query().Get("status") != "" {
		statusParts := strings.Split(r.URL.Query().Get("status"), ",")
		code, err := parseStatus(statusParts[0], http.StatusOK)
		if err != nil {
			badRequest(w, err)
			return
		}
		conn.statusCodeFreq = 100
		if len(statusParts) > 1 {
			conn.statusCodeFreq, _ = strconv.Atoi(statusParts[1])
		}
		if chance(conn.statusCodeFreq) {
			conn.statusCode = code
		}
	}
	if r.URL.Query().Get("slow") != "" {
		slowParts := strings.Split(r.URL.Query().Get("slow"), ",")
		conn.slowc, _ = strconv.Atoi(slowParts[0])
		if len(slowParts) > 2 {
			conn.slowcFreq, _ = strconv.Atoi(slowParts[2])
		}
		if len(slowParts) > 1 {
			conn.slowcSpan, _ = strconv.Atoi(slowParts[1])
			// rand.Intn panics on 0
			if conn.slowcSpan > 0 && chance(conn.slowcFreq) {
				conn.slowc = conn.slowc + rand.Intn(conn.slowcSpan)
			}
		}
	}
	log.Println("Sleeping for", conn.slowc, "ms")
	if !sleep(r.Context(), time.Duration(conn.slowc)*time.Millisecond) {
		return
	}

	if r.URL.Query().Get("reset") != "" {
		hijackAndClose(w)
		return
	}
	respond(w, conn.statusCode, "Returning Statuscode: "+strconv.Itoa(conn.statusCode)+" in "+strconv.Itoa(conn.slowc)+"ms")
}

func main() {
	var (
		addressesInput string
		portsInput     string
		templateFile   string
	)
	flag.StringVar(&addressesInput, "a", "127.0.0.1", "Comma-separated list of addresses")
	flag.StringVar(&portsInput, "p", "8080", "Comma-separated list of ports or port ranges (e.g., 4000-4020)")
	flag.StringVar(&templateFile, "template", "", "Path to HTML template file for the default page (default: built-in)")
	flag.Parse()

	var err error
	if templateFile != "" {
		tmpl, err = template.ParseFiles(templateFile)
	} else {
		tmpl, err = template.New("default").Parse(defaultTemplate)
	}
	if err != nil {
		log.Fatal("Parsing template: ", err)
	}

	// Split the addresses and ports
	addresses := strings.Split(addressesInput, ",")
	portParts := strings.Split(portsInput, ",")
	// Expand port ranges
	var ports []string
	for _, part := range portParts {
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			start, err := strconv.Atoi(rangeParts[0])
			if err != nil {
				fmt.Printf("Invalid port range start: %s\n", rangeParts[0])
				continue
			}
			end, err := strconv.Atoi(rangeParts[1])
			if err != nil {
				fmt.Printf("Invalid port range end: %s\n", rangeParts[1])
				continue
			}
			for p := start; p <= end; p++ {
				ports = append(ports, strconv.Itoa(p))
			}
		} else {
			ports = append(ports, part)
		}
	}
	var wg sync.WaitGroup
	for _, address := range addresses {
		for _, port := range ports {
			fullAddr := fmt.Sprintf("%s:%s", address, port)
			wg.Add(1)
			go func(addr string) {
				defer wg.Done()
				mux := http.NewServeMux()
				finalHandler := http.HandlerFunc(defaultHandler)
				mux.HandleFunc("/slow", slowHandler)
				mux.HandleFunc("/error", errorHandler)
				mux.HandleFunc("/reset", resetConnectionHandler)
				mux.HandleFunc("/new", newHandler)
				mux.Handle("/", addHeaders(finalHandler)) // Register the default handler
				log.Println("Starting server on", addr)
				log.Fatal(http.ListenAndServe(addr, mux))
			}(fullAddr)
		}
	}
	log.Println("Number of servers:", len(addresses)*len(ports))
	wg.Wait() // Wait for all servers to finish
}
