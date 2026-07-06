// Command healthcheck is a minimal HTTP health probe for distroless
// containers. It performs a GET request to the URL passed via -url and
// exits 0 on HTTP 200, 1 otherwise. Built into every service image so
// Docker healthchecks work without a shell or wget.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	os.Exit(run())
}

func run() int {
	url := flag.String("url", "http://localhost:8080/healthz", "health endpoint URL")
	timeout := flag.Duration("timeout", 3*time.Second, "request timeout")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Get(*url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
