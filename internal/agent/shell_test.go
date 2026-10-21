package agent

import (
	"runtime"
	"strings"
	"testing"
)

func TestExecuteShellCommandReturnsCombinedOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("remote shell commands run on Linux rigs")
	}
	output, err := executeShellCommand("printf stdout; printf stderr >&2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "stdout") || !strings.Contains(output, "stderr") {
		t.Fatalf("combined shell output = %q", output)
	}
}

func TestExecuteShellCommandReportsFailureAndBoundsOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("remote shell commands run on Linux rigs")
	}
	output, err := executeShellCommand("printf failure; exit 3")
	if err == nil || !strings.Contains(output, "failure") {
		t.Fatalf("failed shell command output = %q, error = %v", output, err)
	}
	output, err = executeShellCommand("head -c 70000 /dev/zero | tr '\\000' x")
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != shellCommandOutputLimit {
		t.Fatalf("bounded shell output has %d bytes, want %d", len(output), shellCommandOutputLimit)
	}
}

func TestExecuteShellCommandRejectsInvalidInput(t *testing.T) {
	if _, err := executeShellCommand("  "); err == nil {
		t.Fatal("accepted empty command")
	}
	if _, err := executeShellCommand(strings.Repeat("x", 4097)); err == nil {
		t.Fatal("accepted oversized command")
	}
}
