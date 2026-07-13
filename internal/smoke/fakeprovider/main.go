// Command fakeprovider is a deterministic TLS provider used only by the
// full-stack Compose acceptance test.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const maximumFetchBytes = 8 << 20

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("fake-provider: command is required")
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "healthcheck":
		return healthcheck()
	case "fetch":
		return fetch(args[1:], output)
	default:
		return fmt.Errorf("fake-provider: unknown command %q", args[0])
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("address", ":8443", "TLS listen address")
	certificate := flags.String("cert", "/run/smoke/provider.crt", "TLS certificate")
	privateKey := flags.String("key", "/run/smoke/provider.key", "TLS private key")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("fake-provider: invalid serve arguments")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"status":"ok"}`)
	})
	mux.HandleFunc("POST /v1/responses", providerResponse)
	server := &http.Server{
		Addr: *address, Handler: mux, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return server.ListenAndServeTLS(*certificate, *privateKey)
}

func providerResponse(writer http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		http.Error(writer, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
		return
	}
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || strings.TrimSpace(fmt.Sprint(payload["model"])) == "" {
		http.Error(writer, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Fake-Provider", "harden-llm-smoke")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"id": "resp_smoke", "object": "response", "status": "completed", "output_text": "smoke-ok",
		"usage": map[string]any{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
	})
}

func healthcheck() error {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	connection, err := tls.DialWithDialer(dialer, "tcp", "127.0.0.1:8443", &tls.Config{
		MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, // Test-only process-local readiness probe.
	})
	if err != nil {
		return fmt.Errorf("fake-provider: healthcheck: %w", err)
	}
	return connection.Close()
}

func fetch(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("fetch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	target := flags.String("url", "", "internal HTTP URL")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !strings.HasPrefix(*target, "http://") {
		return errors.New("fake-provider: fetch requires one internal http:// URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, *target, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumFetchBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maximumFetchBytes {
		return errors.New("fake-provider: fetched response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("fake-provider: fetch returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	_, err = output.Write(body)
	return err
}
