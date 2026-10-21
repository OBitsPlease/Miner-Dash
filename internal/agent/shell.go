package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const shellCommandOutputLimit = 64 * 1024

func executeShellCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > 4096 {
		return "", errors.New("shell command must contain 1 to 4096 characters")
	}
	if runtime.GOOS != "linux" {
		return "", errors.New("remote shell commands are supported only on Linux rigs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "-lc", command)
	output := &boundedBuffer{limit: shellCommandOutputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if ctx.Err() != nil {
		return output.String(), fmt.Errorf("shell command timed out after 60 seconds: %w", ctx.Err())
	}
	if err != nil {
		return output.String(), fmt.Errorf("shell command failed: %w", err)
	}
	return output.String(), nil
}
