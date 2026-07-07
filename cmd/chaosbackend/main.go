package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/auduny/chaosbackend"
)

func main() {
	var (
		addressesInput string
		portsInput     string
		templateFile   string
	)
	flag.StringVar(&addressesInput, "a", "127.0.0.1", "Comma-separated list of addresses")
	flag.StringVar(&portsInput, "p", "8080", "Comma-separated list of ports or port ranges (e.g., 4000-4020)")
	flag.StringVar(&templateFile, "template", "", "Path to HTML template file for the default page (default: embedded template)")
	flag.Parse()

	srv, err := chaosbackend.New(chaosbackend.Config{TemplateFile: templateFile})
	if err != nil {
		log.Fatal(err)
	}
	mux := srv.Mux()

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
				log.Println("Starting server on", addr)
				log.Fatal(http.ListenAndServe(addr, mux))
			}(fullAddr)
		}
	}
	log.Println("Number of servers:", len(addresses)*len(ports))
	wg.Wait() // Wait for all servers to finish
}
