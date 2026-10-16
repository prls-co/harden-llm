package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/prls-co/harden-llm/httpclient"
	"io"
	"os"
	"path/filepath"
	"time"
)

func runJobWait(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("wait-job", flag.ContinueOnError)
	flags.SetOutput(stderr)
	origin := flags.String("base-url", "", "HLLM API origin")
	id := flags.String("job", "", "job UUID")
	timeout := flags.Duration("timeout", 30*time.Minute, "overall deadline, at most two hours")
	output := flags.String("result-file", "", "optional new private NDJSON output file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 || *timeout > 2*time.Hour {
		return errors.New("wait-job timeout must be positive and at most two hours")
	}
	c, err := httpclient.New(httpclient.Config{BaseURL: *origin, APIKey: getenv("HARDEN_LLM_TOKEN")})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	enc := json.NewEncoder(stdout)
	status, err := c.WaitJob(ctx, *id, func(v httpclient.JobStatus) { _ = enc.Encode(v) })
	if err != nil {
		return err
	}
	if *output != "" {
		if _, err = os.Lstat(*output); !os.IsNotExist(err) {
			return errors.New("result-file must be a new path")
		}
		tmp, err := os.CreateTemp(filepath.Dir(*output), ".hllm-job-result-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if err = c.DownloadJobResult(ctx, *id, tmp); err != nil {
			return err
		}
		if err = tmp.Sync(); err != nil {
			return err
		}
		if err = tmp.Close(); err != nil {
			return err
		}
		// Link publishes without overwriting a file created by another process.
		if err = os.Link(tmp.Name(), *output); err != nil {
			return err
		}
	}
	if status.Counts.Failed > 0 {
		return errors.New("batch completed with item errors; inspect job results")
	}
	return nil
}
