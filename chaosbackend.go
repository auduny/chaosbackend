package main

import (
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

func defaultHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("template.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Backend", "default")
	tmpl.Execute(w, "This is the default page.")
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

func slowHandler(w http.ResponseWriter, r *http.Request) {
	sleepBeforeFirstByte, _ := strconv.Atoi(r.URL.Query().Get("sleep"))
	sleepBetweenBytes, _ := strconv.Atoi(r.URL.Query().Get("sleepBetweenBytes"))
	slowResponse(w, time.Duration(sleepBeforeFirstByte)*time.Millisecond, time.Duration(sleepBetweenBytes)*time.Millisecond)
}

func errorHandler(w http.ResponseWriter, r *http.Request) {
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

func resetConnectionHandler(w http.ResponseWriter, r *http.Request) {
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

func addHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backends", "snuskepus")

		next.ServeHTTP(w, r)
	})
}

func main() {
	var (
		addressesInput string
		portsInput     string
	)
	flag.StringVar(&addressesInput, "a", "127.0.0.1", "Comma-separated list of addresses")
	flag.StringVar(&portsInput, "p", "8080", "Comma-separated list of ports or port ranges (e.g., 4000-4020)")
	flag.Parse()

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
				mux.Handle("/", addHeaders(finalHandler)) // Register the default handler
				log.Println("Starting server on", addr)
				log.Fatal(http.ListenAndServe(addr, mux))
			}(fullAddr)
		}
	}
	log.Println("Number of servers:", len(addresses)*len(ports))
	wg.Wait() // Wait for all servers to finish
}
