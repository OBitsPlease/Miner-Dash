package agent

import (
	"errors"
	"os/exec"
	"runtime"
)

const maxCommandOutput = 16 * 1024

func executeSystemAction(action string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("system actions are supported only on Linux agents")
	}
	argument := ""
	switch action {
	case "reboot":
		argument = "reboot"
	case "shutdown":
		argument = "poweroff"
	default:
		return "", errors.New("unsupported system action")
	}
	output, err := exec.Command("/usr/bin/systemctl", argument).CombinedOutput()
	if len(output) > maxCommandOutput {
		output = output[:maxCommandOutput]
	}
	return string(output), err
}
