// Command healthcheck is a zero-dependency HTTP probe used as the distroless
// container HEALTHCHECK. It exits 0 when /healthz responds 200 within a deadline.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
}

func run() error {
	port := envOr("HEALTHCHECK_PORT", "8080")
	path := envOr("HEALTHCHECK_PATH", "/healthz")
	url := "http://localhost:" + port + path

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url) //nolint:noctx // single-shot probe with client timeout
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
