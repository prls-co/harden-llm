package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/gateway/command"
)

const (
	databaseURLEnvironment = "HARDEN_LLM_DATABASE_URL"
	bootstrapTimeout       = 30 * time.Second
	maximumPasswordBytes   = 1024
)

func runBootstrapUser(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("bootstrap-user", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ownerID := flags.String("owner-id", "", "stable owner identifier")
	email := flags.String("email", "", "login email address")
	passwordFile := flags.String("password-file", "-", "password file, or - for standard input")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("bootstrap-user: invalid arguments: %w", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(*ownerID) == "" || strings.TrimSpace(*email) == "" {
		return errors.New("bootstrap-user: --owner-id and --email are required")
	}
	databaseURL := strings.TrimSpace(getenv(databaseURLEnvironment))
	if databaseURL == "" {
		return fmt.Errorf("bootstrap-user: %s is required", databaseURLEnvironment)
	}
	password, err := loadPassword(stdin, *passwordFile)
	if err != nil {
		return err
	}
	commandContext, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()
	user, err := command.BootstrapUser(commandContext, command.BootstrapUserConfig{
		DatabaseURL: databaseURL,
		OwnerID:     *ownerID,
		Email:       *email,
		Password:    password,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}{ID: user.ID, Email: user.Email})
}

func loadPassword(stdin io.Reader, filename string) (string, error) {
	reader := io.Reader(stdin)
	var file *os.File
	if filename != "-" {
		var err error
		file, err = os.Open(filename)
		if err != nil {
			return "", errors.New("bootstrap-user: cannot open password file")
		}
		defer file.Close()
		reader = file
	}
	if reader == nil {
		return "", errors.New("bootstrap-user: password input is required")
	}
	buffered := bufio.NewReader(io.LimitReader(reader, maximumPasswordBytes+2))
	password, readErr := buffered.ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", errors.New("bootstrap-user: cannot read password")
	}
	password = strings.TrimSuffix(password, "\n")
	password = strings.TrimSuffix(password, "\r")
	if len(password) > maximumPasswordBytes {
		return "", errors.New("bootstrap-user: password is too long")
	}
	return password, nil
}
