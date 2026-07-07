package chaosbackend

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type connection struct {
	statusCode     int
	statusCodeFreq int
	slowc          int
	slowcSpan      int
	slowcFreq      int
	slowbb         int
	slowbbSpan     int
	reset          int
	resetFreq      int
}

func slowResponse(w http.ResponseWriter, sleepBeforeFirstByte time.Duration, sleepBetweenBytes time.Duration) {
	content := "Example content delivered slowly. Connect:" + sleepBeforeFirstByte.String() + " Betweenbytes:" + sleepBetweenBytes.String()
	log.Println(content)
	time.Sleep(sleepBeforeFirstByte)
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}
	for _, c := range content {
		fmt.Fprintf(w, "%c", c)
		flusher.Flush() // Manually flush the buffer
		time.Sleep(sleepBetweenBytes)
	}
}

// SlowHandler serves "/slow": it streams a canned response one byte at a
// time, with configurable delays before the first byte and between bytes.
func SlowHandler(w http.ResponseWriter, r *http.Request) {
	sleepBeforeFirstByte, _ := strconv.Atoi(r.URL.Query().Get("sleep"))
	sleepBetweenBytes, _ := strconv.Atoi(r.URL.Query().Get("sleepBetweenBytes"))
	slowResponse(w, time.Duration(sleepBeforeFirstByte)*time.Millisecond, time.Duration(sleepBetweenBytes)*time.Millisecond)
}

// ErrorHandler serves "/error": it returns an arbitrary HTTP status code,
// optionally after a delay.
func ErrorHandler(w http.ResponseWriter, r *http.Request) {
	statusCode, _ := strconv.Atoi(r.URL.Query().Get("status"))
	if statusCode == 0 {
		// Default to 500
		statusCode = 500
	}
	sleepBeforeFirstByte, _ := strconv.Atoi(r.URL.Query().Get("sleep"))
	log.Println("Returning Statuscode:", statusCode, "Sleeping for", sleepBeforeFirstByte, "ms")
	time.Sleep(time.Duration(sleepBeforeFirstByte) * time.Millisecond)
	http.Error(w, "Returning Statuscode: "+strconv.Itoa(statusCode), statusCode)
}

// ResetHandler serves "/reset": it hijacks and immediately closes the
// underlying TCP connection, simulating a connection reset.
func ResetHandler(w http.ResponseWriter, r *http.Request) {
	// Take over the connection
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Printf("Hijacking failed: %v\n", err)
		http.Error(w, "Hijacking failed", http.StatusInternalServerError)
		return
	}

	// Close the connection immediately
	conn.Close()
}

// FaultHandler serves "/new": a combined, probabilistic fault injector
// driven by the status, slow, and reset query parameters.
func FaultHandler(w http.ResponseWriter, r *http.Request) {
	conn := connection{statusCode: 200, statusCodeFreq: 0, slowc: 0, slowcSpan: 0, slowcFreq: 10, slowbbSpan: 0, reset: 0, resetFreq: 0}
	if r.URL.Query().Get("status") != "" {
		statusParts := strings.Split(r.URL.Query().Get("status"), ",")
		if len(statusParts) > 1 {
			randomness := rand.Intn(100)
			conn.statusCodeFreq, _ = strconv.Atoi(statusParts[1])
			log.Println("Frequency:", randomness, "StatusCodeFreq:", conn.statusCodeFreq)
			if randomness <= conn.statusCodeFreq {
				conn.statusCode, _ = strconv.Atoi(statusParts[0])
			}
		} else {
			conn.statusCode, _ = strconv.Atoi(statusParts[0])
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
			randomness := rand.Intn(100)
			log.Println("Frequency:", randomness, "SlowcFreq:", conn.slowcFreq)
			if randomness <= conn.slowcFreq {
				conn.slowc = conn.slowc + rand.Intn(conn.slowcSpan)
			}
		}
	}
	log.Println("Sleeping for", conn.slowc, "ms")
	time.Sleep(time.Duration(conn.slowc) * time.Millisecond)

	if r.URL.Query().Get("reset") != "" {
		thisconnection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			log.Printf("Hijacking failed: %v\n", err)
			http.Error(w, "Hijacking failed", http.StatusInternalServerError)
			return
		}

		// Close the connection immediately
		thisconnection.Close()
	}
	http.Error(w, "Returning Statuscode: "+strconv.Itoa(conn.statusCode)+" in "+strconv.Itoa(conn.slowc)+"ms", conn.statusCode)
}
