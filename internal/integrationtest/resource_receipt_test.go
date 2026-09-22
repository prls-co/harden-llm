package integrationtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-280
func TestResourceReceiptAcceptsValidOwnershipRecord(t *testing.T) {
	if err := ValidateResourceReceipt(validResourceReceipt(t)); err != nil {
		t.Fatalf("valid receipt was rejected: %v", err)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-280
func TestResourceReceiptRejectsInvalidOwnershipRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ResourceReceipt)
	}{
		{name: "wrong schema", mutate: func(receipt *ResourceReceipt) { receipt.SchemaVersion++ }},
		{name: "non-disposable environment", mutate: func(receipt *ResourceReceipt) { receipt.Disposable = false }},
		{name: "foreign project", mutate: func(receipt *ResourceReceipt) { receipt.Project = "production" }},
		{name: "invalid run id", mutate: func(receipt *ResourceReceipt) { receipt.RunID = "../outside" }},
		{name: "invalid source", mutate: func(receipt *ResourceReceipt) { receipt.SourceSHA = "unknown" }},
		{name: "missing daemon", mutate: func(receipt *ResourceReceipt) { receipt.DaemonID = "" }},
		{name: "missing supervisor", mutate: func(receipt *ResourceReceipt) { receipt.SupervisorPID = 0 }},
		{name: "unknown state", mutate: func(receipt *ResourceReceipt) { receipt.State = "started" }},
		{name: "relative compose path", mutate: func(receipt *ResourceReceipt) { receipt.ComposeFiles = []string{"compose.yml"} }},
		{name: "duplicate resource id", mutate: func(receipt *ResourceReceipt) { receipt.ResourceIDs.Containers = []string{"abc123", "abc123"} }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			receipt := validResourceReceipt(t)
			testCase.mutate(&receipt)
			if err := ValidateResourceReceipt(receipt); err == nil {
				t.Fatal("invalid ownership record was accepted")
			}
		})
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-280
func TestResourceReceiptWriteIsPrivateAtomicAndStateful(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure receipt directory: %v", err)
	}
	path, err := WriteResourceReceipt(directory, validResourceReceipt(t))
	if err != nil {
		t.Fatalf("write valid receipt: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat receipt: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("receipt mode = %#o, want 0600", got)
	}
	got, err := ReadResourceReceipt(path)
	if err != nil {
		t.Fatalf("read valid receipt: %v", err)
	}
	if got.Project != "harden-llm-test-0123456789ab" || got.State != "registered" {
		t.Fatalf("receipt identity/state = %q/%q", got.Project, got.State)
	}
	got, err = UpdateResourceReceipt(path, "creating")
	if err != nil {
		t.Fatalf("advance receipt: %v", err)
	}
	if got.State != "creating" {
		t.Fatalf("updated state = %q, want creating", got.State)
	}
	if _, err := UpdateResourceReceipt(path, "cleaned"); err == nil {
		t.Fatal("invalid state transition was accepted")
	}
	if _, err := WriteResourceReceipt(directory, validResourceReceipt(t)); err == nil {
		t.Fatal("duplicate project receipt was overwritten")
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-280
func TestResourceReceiptRejectsSymlinkAndBroadDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure receipt directory: %v", err)
	}
	validPath, err := WriteResourceReceipt(directory, validResourceReceipt(t))
	if err != nil {
		t.Fatalf("write valid receipt: %v", err)
	}
	linkPath := filepath.Join(directory, "resource-link.json")
	if err := os.Symlink(validPath, linkPath); err != nil {
		t.Fatalf("create receipt symlink: %v", err)
	}
	if _, err := ReadResourceReceipt(linkPath); err == nil {
		t.Fatal("receipt symlink was accepted")
	}
	if err := os.Chmod(filepath.Dir(validPath), 0o750); err != nil {
		t.Fatalf("make directory permissive: %v", err)
	}
	if _, err := ReadResourceReceipt(validPath); err == nil {
		t.Fatal("group-accessible receipt directory was accepted")
	}
}

func validResourceReceipt(t *testing.T) ResourceReceipt {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "resource_receipt_valid.json"))
	if err != nil {
		t.Fatalf("read shared resource receipt vector: %v", err)
	}
	var receipt ResourceReceipt
	if err := json.Unmarshal(contents, &receipt); err != nil {
		t.Fatalf("decode shared resource receipt vector: %v", err)
	}
	return receipt
}
