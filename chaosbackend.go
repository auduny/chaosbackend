package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Listen         string `json:"listen"`
	FailOverListen string `json:"failOverListen"`
}

func readConfig() Config {
	file, err := os.Open("config.json")
	if err != nil {
		log.Println("Error opening config file, using default values:", err)
		return Config{Listen: "127.0.0.1:8080", FailOverListen: "127.0.0.1:8081"} // Default values
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	config := Config{}
	err = decoder.Decode(&config)
	if err != nil {
		log.Println("Error decoding config file, using default values:", err)
		return Config{Listen: "127.0.0.1:8080", FailOverListen: "127.0.0.1:8081"} // Default values
	}
	return config
}

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

func main() {
	config := readConfig()
	flagListen := flag.String("listen", config.Listen, "IP to listen on")
	flagFailOverListen := flag.String("failoverlisten", config.FailOverListen, "IP to listen on")

	flag.Parse()
	config.Listen = *flagListen
	config.FailOverListen = *flagFailOverListen

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", slowHandler)
	mux.HandleFunc("/error", errorHandler)
	mux.HandleFunc("/reset", resetConnectionHandler)
	mux.HandleFunc("/", defaultHandler) // Register the default handler
	failover_mux := http.NewServeMux()
	failover_mux.HandleFunc("/", defaultHandler) // Register the default handler
	go func() {
		log.Println("Starting failover server", config.FailOverListen)
		log.Fatal(http.ListenAndServe(config.FailOverListen, failover_mux))
	}()
	log.Println("Starting main server on", config.Listen)

	log.Fatal(http.ListenAndServe(config.Listen, mux))
}
