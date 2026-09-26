package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mock-issue-mcp/internal/mcpserver"
	"mock-issue-mcp/internal/tracker"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	addr := env("LISTEN_ADDR", "127.0.0.1:8090")
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" || port == "0" {
		return errors.New("LISTEN_ADDR must include a nonzero port")
	}
	dataPath := env("DATA_PATH", "data/seed.json")
	if _, err := tracker.Load(dataPath); err != nil {
		return fmt.Errorf("load dataset: %w", err)
	}
	apiURL := env("TRACKER_API_URL", "http://127.0.0.1:"+port)
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid TRACKER_API_URL")
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpserver.Handler(mcpserver.New(apiURL)))
	mux.Handle("/", tracker.NewHandler(dataPath))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		log.Printf("Mock tracker listening on %s; MCP: /mcp; API: /api/issues", addr)
		done <- server.ListenAndServe()
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
