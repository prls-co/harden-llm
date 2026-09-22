package integrationtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ResourceReceipt is the JSON ownership record shared by the Node runner and
// Go Compose fixtures. Keep its JSON contract aligned with
// scripts/test-resource-lifecycle.mjs.
type ResourceReceipt struct {
	SchemaVersion   int         `json:"schemaVersion"`
	Repository      string      `json:"repository"`
	Environment     string      `json:"environment"`
	Disposable      bool        `json:"disposable"`
	RunID           string      `json:"runId"`
	Project         string      `json:"project"`
	SourceSHA       string      `json:"sourceSHA"`
	DaemonID        string      `json:"daemonId"`
	HostBootID      string      `json:"hostBootId"`
	SupervisorPID   int         `json:"supervisorPid"`
	SupervisorStart string      `json:"supervisorStart"`
	State           string      `json:"state"`
	CreatedAt       time.Time   `json:"createdAt"`
	ComposeFiles    []string    `json:"composeFiles"`
	ResourceIDs     ResourceIDs `json:"resourceIds"`
}

// ResourceIDs contains only resources associated with one recorded project.
type ResourceIDs struct {
	Containers []string `json:"containers"`
	Volumes    []string `json:"volumes"`
	Networks   []string `json:"networks"`
}

var (
	resourceIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,190}$`)
	resourceSHAPattern = regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)
	resourceStates     = map[string]map[string]bool{
		"registered":      {"creating": true, "cleaning": true, "cleanup-pending": true},
		"creating":        {"running": true, "cleaning": true, "cleanup-pending": true},
		"running":         {"cleaning": true, "cleanup-pending": true},
		"cleaning":        {"cleaned": true, "cleanup-pending": true},
		"cleanup-pending": {"cleaning": true},
		"cleaned":         {"cleanup-pending": true},
	}
)

// ValidateResourceReceipt rejects malformed, non-test, and ambiguous ownership
// records before they can authorize Docker mutation or recovery.
func ValidateResourceReceipt(receipt ResourceReceipt) error {
	if receipt.SchemaVersion != 1 {
		return errors.New("resource receipt schemaVersion must be 1")
	}
	if receipt.Repository != "harden-llm" || receipt.Environment != "test" || !receipt.Disposable {
		return errors.New("resource receipt must describe disposable Harden-LLM test resources")
	}
	for name, value := range map[string]string{
		"runId": receipt.RunID, "project": receipt.Project, "daemonId": receipt.DaemonID,
		"hostBootId": receipt.HostBootID, "supervisorStart": receipt.SupervisorStart,
	} {
		if !resourceIDPattern.MatchString(value) {
			return fmt.Errorf("resource receipt %s is invalid", name)
		}
	}
	if !strings.HasPrefix(receipt.Project, "harden-llm-") {
		return errors.New("resource receipt project is outside the test namespace")
	}
	if !resourceSHAPattern.MatchString(receipt.SourceSHA) {
		return errors.New("resource receipt sourceSHA is invalid")
	}
	if receipt.SupervisorPID <= 0 {
		return errors.New("resource receipt supervisorPid is invalid")
	}
	if receipt.CreatedAt.IsZero() {
		return errors.New("resource receipt createdAt is invalid")
	}
	if _, ok := resourceStates[receipt.State]; !ok {
		return errors.New("resource receipt state is invalid")
	}
	if len(receipt.ComposeFiles) == 0 {
		return errors.New("resource receipt composeFiles must not be empty")
	}
	for _, composeFile := range receipt.ComposeFiles {
		if !filepath.IsAbs(composeFile) || strings.ContainsRune(composeFile, '\x00') {
			return errors.New("resource receipt composeFiles must contain absolute paths")
		}
	}
	for name, values := range map[string][]string{
		"containers": receipt.ResourceIDs.Containers, "volumes": receipt.ResourceIDs.Volumes,
		"networks": receipt.ResourceIDs.Networks,
	} {
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if !resourceIDPattern.MatchString(value) {
				return fmt.Errorf("resource receipt %s contains an invalid ID", name)
			}
			if _, exists := seen[value]; exists {
				return fmt.Errorf("resource receipt %s contains a duplicate ID", name)
			}
			seen[value] = struct{}{}
		}
	}
	return nil
}

// WriteResourceReceipt durably creates one private receipt before a fixture
// mutates Docker. The returned filename is stable for the Compose project.
func WriteResourceReceipt(directory string, receipt ResourceReceipt) (string, error) {
	if err := ValidateResourceReceipt(receipt); err != nil {
		return "", err
	}
	if err := ensurePrivateResourceDirectory(directory); err != nil {
		return "", err
	}
	name := "resource-" + receipt.Project + ".json"
	if !resourceReceiptFilenamePattern.MatchString(name) {
		return "", errors.New("resource receipt project cannot be used as a filename")
	}
	return writeResourceReceipt(filepath.Join(directory, receipt.RunID, name), receipt, true)
}

var resourceReceiptFilenamePattern = regexp.MustCompile(`^resource-[A-Za-z0-9_.:-]{1,200}\.json$`)

// ReadResourceReceipt refuses symlinks, broad permissions, and invalid content.
func ReadResourceReceipt(receiptPath string) (ResourceReceipt, error) {
	if err := ensurePrivateResourceDirectory(filepath.Dir(receiptPath)); err != nil {
		return ResourceReceipt{}, err
	}
	info, err := os.Lstat(receiptPath)
	if err != nil {
		return ResourceReceipt{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return ResourceReceipt{}, errors.New("resource receipt permissions or file type are not private")
	}
	contents, err := os.ReadFile(receiptPath)
	if err != nil {
		return ResourceReceipt{}, err
	}
	var receipt ResourceReceipt
	if err := json.Unmarshal(contents, &receipt); err != nil {
		return ResourceReceipt{}, fmt.Errorf("decode resource receipt: %w", err)
	}
	if err := ValidateResourceReceipt(receipt); err != nil {
		return ResourceReceipt{}, err
	}
	return receipt, nil
}

// UpdateResourceReceipt atomically advances a receipt through allowed states.
func UpdateResourceReceipt(receiptPath, state string) (ResourceReceipt, error) {
	receipt, err := ReadResourceReceipt(receiptPath)
	if err != nil {
		return ResourceReceipt{}, err
	}
	if !resourceStates[receipt.State][state] {
		return ResourceReceipt{}, fmt.Errorf("invalid resource receipt transition %s -> %s", receipt.State, state)
	}
	receipt.State = state
	if err := ValidateResourceReceipt(receipt); err != nil {
		return ResourceReceipt{}, err
	}
	_, err = writeResourceReceipt(receiptPath, receipt, false)
	return receipt, err
}

// RegisterResourceReceipt creates a Go-fixture receipt using the identity
// already established by the managed runner environment. It resolves the
// actual daemon/source/process identities before returning to the caller.
func RegisterResourceReceipt(project string, composeFiles []string) (string, error) {
	directory := strings.TrimSpace(os.Getenv("HARDEN_LLM_TEST_RESOURCE_DIR"))
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve private resource receipt directory: %w", err)
		}
		directory = filepath.Join(home, ".local", "state", "harden-llm", "test-resources")
	}
	runID := strings.TrimSpace(os.Getenv("HARDEN_LLM_TEST_RUN_ID"))
	if runID == "" {
		return "", errors.New("Compose fixtures must run through scripts/run-test-tier.mjs so ownership is recorded")
	}
	daemonID, err := dockerDaemonIdentity()
	if err != nil {
		return "", err
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	output, commandErr := command.Output()
	if commandErr != nil {
		return "", fmt.Errorf("identify source revision for Compose receipt: %w", commandErr)
	}
	sourceSHA := strings.TrimSpace(string(output))
	hostBootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read host boot identity for Compose receipt: %w", err)
	}
	supervisorPID, err := strconv.Atoi(strings.TrimSpace(os.Getenv("HARDEN_LLM_TEST_SUPERVISOR_PID")))
	if err != nil || supervisorPID <= 0 {
		return "", errors.New("Compose fixtures must inherit a valid managed runner supervisor identity")
	}
	supervisorStart := strings.TrimSpace(os.Getenv("HARDEN_LLM_TEST_SUPERVISOR_START"))
	if supervisorStart == "" {
		return "", errors.New("Compose fixtures must inherit a managed runner process-start identity")
	}
	currentSupervisorStart, err := processStartIdentity(supervisorPID)
	if err != nil {
		return "", err
	}
	if currentSupervisorStart != supervisorStart {
		return "", errors.New("managed runner supervisor identity changed before Compose receipt registration")
	}
	absoluteComposeFiles := make([]string, 0, len(composeFiles))
	for _, composeFile := range composeFiles {
		absolute, err := filepath.Abs(composeFile)
		if err != nil {
			return "", fmt.Errorf("resolve Compose file for receipt: %w", err)
		}
		absoluteComposeFiles = append(absoluteComposeFiles, absolute)
	}
	receipt := ResourceReceipt{
		SchemaVersion: 1, Repository: "harden-llm", Environment: "test", Disposable: true,
		RunID: runID, Project: project, SourceSHA: sourceSHA, DaemonID: daemonID,
		HostBootID: strings.TrimSpace(string(hostBootID)), SupervisorPID: supervisorPID,
		SupervisorStart: supervisorStart, State: "registered", CreatedAt: time.Now().UTC(),
		ComposeFiles: absoluteComposeFiles,
		ResourceIDs:  ResourceIDs{Containers: []string{}, Volumes: []string{}, Networks: []string{}},
	}
	return WriteResourceReceipt(directory, receipt)
}

// AdvanceResourceReceipt is the fixture-facing state transition helper.
func AdvanceResourceReceipt(receiptPath, state string) error {
	_, err := UpdateResourceReceipt(receiptPath, state)
	return err
}

func dockerDaemonIdentity() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("identify Docker daemon before Compose mutation: %w", err)
	}
	identity := strings.TrimSpace(string(output))
	if identity == "" || !resourceIDPattern.MatchString(identity) {
		return "", errors.New("Docker returned no valid daemon identity")
	}
	return identity, nil
}

func processStartIdentity(pid int) (string, error) {
	contents, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", fmt.Errorf("read supervisor process identity: %w", err)
	}
	closingParenthesis := strings.LastIndex(string(contents), ")")
	if closingParenthesis < 0 {
		return "", errors.New("cannot parse supervisor process identity")
	}
	fields := strings.Fields(string(contents[closingParenthesis+1:]))
	if len(fields) <= 19 || fields[19] == "" {
		return "", errors.New("cannot establish supervisor process start identity")
	}
	return fields[19], nil
}

func writeResourceReceipt(receiptPath string, receipt ResourceReceipt, exclusive bool) (string, error) {
	directory := filepath.Dir(receiptPath)
	if err := ensurePrivateResourceDirectory(directory); err != nil {
		return "", err
	}
	if existing, err := os.Lstat(receiptPath); err == nil {
		if !existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0 || existing.Mode().Perm()&0o077 != 0 {
			return "", errors.New("resource receipt target is not a private regular file")
		}
		if exclusive {
			return "", errors.New("resource receipt already exists")
		}
	} else if !errors.Is(err, os.ErrNotExist) || !exclusive {
		return "", fmt.Errorf("inspect resource receipt target: %w", err)
	}

	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate resource receipt temporary name: %w", err)
	}
	temporary := filepath.Join(directory, ".resource-"+hex.EncodeToString(nonce[:])+".tmp")
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create resource receipt temporary file: %w", err)
	}
	contents, marshalErr := json.Marshal(receipt)
	if marshalErr == nil {
		_, marshalErr = file.Write(append(contents, '\n'))
	}
	if marshalErr == nil {
		marshalErr = file.Sync()
	}
	closeErr := file.Close()
	if marshalErr != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("write resource receipt: %w", marshalErr)
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("close resource receipt: %w", closeErr)
	}
	if exclusive {
		if err := os.Link(temporary, receiptPath); err != nil {
			_ = os.Remove(temporary)
			return "", fmt.Errorf("commit resource receipt without overwrite: %w", err)
		}
		if err := os.Remove(temporary); err != nil {
			return "", fmt.Errorf("remove resource receipt temporary link: %w", err)
		}
	} else if err := os.Rename(temporary, receiptPath); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("commit resource receipt update: %w", err)
	}
	if err := os.Chmod(receiptPath, 0o600); err != nil {
		return "", fmt.Errorf("secure resource receipt: %w", err)
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return "", fmt.Errorf("open resource receipt directory: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return "", fmt.Errorf("sync resource receipt directory: %w", err)
	}
	return receiptPath, nil
}

func ensurePrivateResourceDirectory(directory string) error {
	if directory == "" {
		return errors.New("resource receipt directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create resource receipt directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("resource receipt directory must be private (mode=%#o directory=%t symlink=%t)", info.Mode().Perm(), info.IsDir(), info.Mode()&os.ModeSymlink != 0)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return errors.New("resource receipt directory is not owned by the current user")
	}
	return nil
}
