package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-404

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestOnlyServeHealthcheckAndVersionCommandsExist(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &output, &output, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if output.String() != version+"\n" {
		t.Fatalf("version output = %q", output.String())
	}
	for _, args := range [][]string{{"sync-profiles"}, {"audit-artifacts"}, {"bootstrap-user"}, {"unknown"}} {
		output.Reset()
		err := run(context.Background(), args, &output, &output, func(string) string { return "" })
		if err == nil || !strings.Contains(err.Error(), "unknown command") || output.Len() != 0 {
			t.Errorf("removed command %v result = %v, output=%q", args, err, output.String())
		}
	}
	if err := run(context.Background(), []string{"version", "unexpected"}, &output, &output, func(string) string { return "" }); err == nil {
		t.Fatal("version command accepted extra arguments")
	}
}
