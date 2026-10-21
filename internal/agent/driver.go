package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"minerdash/internal/protocol"
)

const hiveOSNVIDIAPackage = "hiveos-nvidia-driver"

var hiveOSNVIDIAVersionPattern = regexp.MustCompile(`\b[0-9]{3}\.[0-9]+(?:\.[0-9]+)?\b`)

func executeGPUDriverUpdate(request protocol.DriverUpdateRequest) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("GPU driver updates are supported only on Linux agents")
	}
	if request.Channel != "stable" {
		return "", errors.New("only the stable distribution driver channel is supported")
	}
	vendor := strings.ToUpper(strings.TrimSpace(request.Vendor))
	if vendor != "NVIDIA" && vendor != "AMD" && vendor != "INTEL" {
		return "", errors.New("GPU driver vendor must be NVIDIA, AMD, or Intel")
	}
	if !isUbuntuFamily(readTrimmed("/etc/os-release")) {
		return "", errors.New("stable GPU driver updates currently require an Ubuntu-family Rig OS")
	}
	if _, err := os.Stat("/usr/bin/apt-get"); err != nil {
		return "", errors.New("apt-get is required for stable GPU driver updates")
	}
	if err := validateDriverSelection(request); err != nil {
		return "", err
	}

	var output strings.Builder
	hiveOS := isHiveOSKernel(commandOutput("uname", "-r"))
	commandName, arguments, err := selectedDriverInstallCommand(request, hiveOS)
	if err != nil {
		return "", err
	}
	if hiveOS && vendor == "NVIDIA" {
		commandName, err = exec.LookPath(commandName)
		if err != nil {
			return "", errors.New("nvidia-driver-update is required for supported HiveOS NVIDIA updates")
		}
		if err := runDriverCommand(&output, commandName, arguments...); err != nil {
			return output.String(), err
		}
		fmt.Fprintf(&output, "\nHiveOS NVIDIA driver %s installed. Reboot this rig from Miner Dash to load it.", request.Version)
		return output.String(), nil
	}
	if err := runDriverCommand(&output, "/usr/bin/apt-get", "update"); err != nil {
		return output.String(), err
	}
	if err := runDriverCommand(&output, commandName, arguments...); err != nil {
		return output.String(), err
	}
	fmt.Fprintf(&output, "\n%s version %s installed. Reboot this rig from Miner Dash to load it.", request.Package, request.Version)
	return output.String(), nil
}

func selectedDriverInstallCommand(request protocol.DriverUpdateRequest, hiveOS bool) (string, []string, error) {
	if err := validateDriverSelection(request); err != nil {
		return "", nil, err
	}
	vendor := strings.ToUpper(strings.TrimSpace(request.Vendor))
	packageName := strings.TrimSpace(request.Package)
	version := strings.TrimSpace(request.Version)
	if hiveOS && vendor == "NVIDIA" {
		if packageName != hiveOSNVIDIAPackage {
			return "", nil, errors.New("HiveOS NVIDIA updates require a version returned by nvidia-driver-update")
		}
		return "nvidia-driver-update", []string{version}, nil
	}
	if packageName == hiveOSNVIDIAPackage {
		return "", nil, errors.New("the selected HiveOS NVIDIA driver is not valid on this rig")
	}
	return "/usr/bin/apt-get", []string{"install", "-y", "--", packageName + "=" + version}, nil
}

func listGPUDriverPackages(request protocol.DriverUpdateRequest) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("GPU driver discovery is supported only on Linux agents")
	}
	vendor := strings.ToUpper(strings.TrimSpace(request.Vendor))
	if vendor != "NVIDIA" && vendor != "AMD" && vendor != "INTEL" {
		return "", errors.New("GPU driver vendor must be NVIDIA, AMD, or Intel")
	}
	if !isUbuntuFamily(readTrimmed("/etc/os-release")) {
		return "", errors.New("GPU driver discovery currently requires an Ubuntu-family Rig OS")
	}
	if vendor == "NVIDIA" && isHiveOSKernel(commandOutput("uname", "-r")) {
		path, err := exec.LookPath("nvidia-driver-update")
		if err != nil {
			return "", errors.New("nvidia-driver-update is required for supported HiveOS NVIDIA updates")
		}
		data, err := exec.Command(path, "--list").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("nvidia-driver-update list failed: %w: %s", err, strings.TrimSpace(string(data)))
		}
		return formatHiveOSNVIDIAVersions(string(data))
	}
	if _, err := os.Stat("/usr/bin/apt-cache"); err != nil {
		return "", errors.New("apt-cache is required for GPU driver discovery")
	}
	packages := map[string][]string{
		"NVIDIA": nil,
		"AMD":    {"linux-firmware", "amdgpu-dkms"},
		"INTEL":  {"linux-firmware", "intel-media-va-driver", "intel-media-va-driver-non-free", "intel-level-zero-gpu"},
	}[vendor]
	if vendor == "NVIDIA" {
		data, err := exec.Command("/usr/bin/apt-cache", "pkgnames", "nvidia-driver-").Output()
		if err != nil {
			return "", fmt.Errorf("list NVIDIA driver packages: %w", err)
		}
		packages = parseNVIDIAPackageNames(data)
	}
	var output strings.Builder
	for _, packageName := range packages {
		command := exec.Command("/usr/bin/apt-cache", "madison", packageName)
		data, err := command.Output()
		if err != nil {
			continue
		}
		output.WriteString(formatAPTDriverPackages(data))
	}
	if output.Len() == 0 {
		return "", errors.New("no driver package versions are available in the configured apt repositories")
	}
	return output.String(), nil
}

func parseNVIDIAPackageNames(output []byte) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, line := range strings.Split(string(output), "\n") {
		packageName := strings.TrimSpace(line)
		if !validDriverPackageForVendor("NVIDIA", packageName) {
			continue
		}
		if _, exists := seen[packageName]; exists {
			continue
		}
		seen[packageName] = struct{}{}
		result = append(result, packageName)
	}
	return result
}

func formatAPTDriverPackages(output []byte) string {
	var result strings.Builder
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 2 {
			continue
		}
		packageName := strings.TrimSpace(fields[0])
		version := strings.TrimSpace(fields[1])
		if packageName != "" && version != "" {
			fmt.Fprintf(&result, "%s|%s\n", packageName, version)
		}
	}
	return result.String()
}

func validateDriverSelection(request protocol.DriverUpdateRequest) error {
	vendor := strings.ToUpper(strings.TrimSpace(request.Vendor))
	packageName := strings.TrimSpace(request.Package)
	version := strings.TrimSpace(request.Version)
	if packageName == "" || version == "" {
		return errors.New("an exact GPU driver package and version are required")
	}
	if !validDriverPackageForVendor(vendor, packageName) {
		return fmt.Errorf("package %q is not a supported %s driver package", packageName, vendor)
	}
	if strings.HasPrefix(version, "-") || strings.ContainsAny(version, " \t\r\n/\\") {
		return errors.New("GPU driver version contains unsupported characters")
	}
	return nil
}

func validDriverPackageForVendor(vendor, packageName string) bool {
	switch vendor {
	case "NVIDIA":
		if packageName == hiveOSNVIDIAPackage {
			return true
		}
		suffix, found := strings.CutPrefix(packageName, "nvidia-driver-")
		if !found || suffix == "" {
			return false
		}
		for _, character := range suffix {
			if character < '0' || character > '9' {
				return false
			}
		}
		return true
	case "AMD":
		return packageName == "linux-firmware" || packageName == "amdgpu-dkms"
	case "INTEL":
		return packageName == "linux-firmware" ||
			packageName == "intel-media-va-driver" ||
			packageName == "intel-media-va-driver-non-free" ||
			packageName == "intel-level-zero-gpu"
	default:
		return false
	}
}

func formatHiveOSNVIDIAVersions(output string) (string, error) {
	versions := hiveOSNVIDIAVersionPattern.FindAllString(cleanANSI(output), -1)
	seen := make(map[string]struct{}, len(versions))
	var result strings.Builder
	for _, version := range versions {
		if _, exists := seen[version]; exists {
			continue
		}
		seen[version] = struct{}{}
		fmt.Fprintf(&result, "%s|%s\n", hiveOSNVIDIAPackage, version)
	}
	if result.Len() == 0 {
		return "", errors.New("nvidia-driver-update returned no installable driver versions")
	}
	return result.String(), nil
}

func cleanANSI(value string) string {
	const escape = '\x1b'
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != escape || index+1 >= len(value) || value[index+1] != '[' {
			result.WriteByte(value[index])
			continue
		}
		index += 2
		for index < len(value) && (value[index] < '@' || value[index] > '~') {
			index++
		}
	}
	return result.String()
}

func isHiveOSKernel(kernel string) bool {
	return strings.Contains(strings.ToLower(kernel), "hiveos")
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
