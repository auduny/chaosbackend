package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type Config struct {
	MainListen     string `json:"mainListen"`
	FailOverListen string `json:"failOverListen"`
}

func readConfig() Config {
	file, err := os.Open("config.json")
	if err != nil {
		log.Println("Error opening config file, using default values:", err)
		return Config{MainListen: "127.0.0.1:8080", FailOverListen: "127.0.0.1:8081"} // Default values
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	config := Config{}
	err = decoder.Decode(&config)
	if err != nil {
		log.Println("Error decoding config file, using default values:", err)
		return Config{MainListen: "127.0.0.1:8080", FailOverListen: "127.0.0.1:8081"} // Default values
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
	log.Println("Done")
}

func slowHandler(w http.ResponseWriter, r *http.Request) {
	sleepBeforeFirstByte, _ := strconv.Atoi(r.URL.Query().Get("sleepBeforeFirstByte"))
	sleepBetweenBytes, _ := strconv.Atoi(r.URL.Query().Get("sleepBetweenBytes"))
	slowResponse(w, time.Duration(sleepBeforeFirstByte)*time.Millisecond, time.Duration(sleepBetweenBytes)*time.Millisecond)
}

func errorHandler(w http.ResponseWriter, r *http.Request) {
	statusCode, _ := strconv.Atoi(r.URL.Query().Get("statusCode"))
	if statusCode == 0 {
		http.Error(w, "This is an error", http.StatusInternalServerError)
	} else {
		http.Error(w, "Returning Error Statuscode: "+strconv.Itoa(statusCode), statusCode)
	}
}

func main() {
	config := readConfig()
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", slowHandler)
	mux.HandleFunc("/error", errorHandler)
	mux.HandleFunc("/", defaultHandler) // Register the default handler
	failover_mux := http.NewServeMux()
	failover_mux.HandleFunc("/", defaultHandler) // Register the default handler
	go func() {
		log.Println("Starting failover server", config.FailOverListen)
		log.Fatal(http.ListenAndServe(config.FailOverListen, failover_mux))
	}()
	log.Println("Starting main server on", config.MainListen)
	log.Fatal(http.ListenAndServe(config.MainListen, mux))
}
