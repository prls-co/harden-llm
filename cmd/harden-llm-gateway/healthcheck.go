package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultHealthcheckURL = "http://127.0.0.1:8080/readyz"
	healthcheckTimeout    = 2 * time.Second
	healthcheckBodyLimit  = 4 << 10
)

func runHealthcheck(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("url", defaultHealthcheckURL, "loopback readiness URL")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("healthcheck: usage: healthcheck [--url http://127.0.0.1:8080/readyz]")
	}
	parsed, err := url.Parse(*endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("healthcheck: URL must be a plain HTTP loopback URL")
	}
	host := parsed.Hostname()
	address := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (address == nil || !address.IsLoopback()) {
		return errors.New("healthcheck: URL must use a loopback host")
	}
	requestContext, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("healthcheck: build request: %w", err)
	}
	client := &http.Client{
		Timeout: healthcheckTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("healthcheck: readiness request failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, healthcheckBodyLimit))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: readiness returned %s", response.Status)
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, "ready\n")
	}
	return nil
}
