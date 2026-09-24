package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"minerdash/internal/protocol"
)

func executeGPUDriverUpdate(request protocol.DriverUpdateRequest) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("GPU driver updates are supported only on Linux agents")
	}
	if request.Channel != "stable" {
		return "", errors.New("only the stable distribution driver channel is supported")
	}
	vendor := strings.ToUpper(strings.TrimSpace(request.Vendor))
	if vendor != "NVIDIA" && vendor != "AMD" {
		return "", errors.New("GPU driver vendor must be NVIDIA or AMD")
	}
	if !isUbuntuFamily(readTrimmed("/etc/os-release")) {
		return "", errors.New("stable GPU driver updates currently require an Ubuntu-family Rig OS")
	}
	if _, err := os.Stat("/usr/bin/apt-get"); err != nil {
		return "", errors.New("apt-get is required for stable GPU driver updates")
	}

	var output strings.Builder
	if err := runDriverCommand(&output, "/usr/bin/apt-get", "update"); err != nil {
		return output.String(), err
	}
	switch vendor {
	case "NVIDIA":
		path, err := exec.LookPath("ubuntu-drivers")
		if err != nil {
			return output.String(), errors.New("ubuntu-drivers is required for distribution-approved NVIDIA updates")
		}
		if err := runDriverCommand(&output, path, "install"); err != nil {
			return output.String(), err
		}
	case "AMD":
		packages := []string{"linux-firmware"}
		if packageInstalled("amdgpu-dkms") {
			packages = append(packages, "amdgpu-dkms")
		}
		arguments := append([]string{"install", "--only-upgrade", "-y"}, packages...)
		if err := runDriverCommand(&output, "/usr/bin/apt-get", arguments...); err != nil {
			return output.String(), err
		}
	}
	output.WriteString("\nDriver packages updated. Reboot this rig from Miner Dash to load the new driver.")
	return output.String(), nil
}

func isUbuntuFamily(osRelease string) bool {
	for _, line := range strings.Split(osRelease, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || (key != "ID" && key != "ID_LIKE") {
			continue
		}
		for _, id := range strings.Fields(strings.Trim(value, `"'`)) {
			if id == "ubuntu" {
				return true
			}
		}
	}
	return false
}

func packageInstalled(name string) bool {
	command := exec.Command("/usr/bin/dpkg-query", "-W", "-f=${db:Status-Status}", name)
	output, err := command.Output()
	return err == nil && strings.TrimSpace(string(output)) == "installed"
}

func runDriverCommand(output *strings.Builder, name string, arguments ...string) error {
	command := exec.Command(name, arguments...)
	command.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	data, err := command.CombinedOutput()
	if output.Len() > 0 {
		output.WriteByte('\n')
	}
	remaining := maxCommandOutput - output.Len()
	if remaining <= 0 {
		data = nil
	} else if len(data) > remaining {
		data = data[:remaining]
	}
	output.Write(data)
	if err != nil {
		return fmt.Errorf("%s failed: %w", filepathBase(name), err)
	}
	return nil
}

func filepathBase(path string) string {
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[index+1:]
	}
	return path
}
