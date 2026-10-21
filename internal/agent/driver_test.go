package agent

import (
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

func TestIsUbuntuFamily(t *testing.T) {
	tests := []struct {
		name    string
		release string
		want    bool
	}{
		{name: "ubuntu", release: "ID=ubuntu\n", want: true},
		{name: "ubuntu derivative", release: "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", want: true},
		{name: "debian", release: "ID=debian\nID_LIKE=debian\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isUbuntuFamily(test.release); got != test.want {
				t.Fatalf("isUbuntuFamily() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIsHiveOSKernel(t *testing.T) {
	tests := []struct {
		name   string
		kernel string
		want   bool
	}{
		{name: "HiveOS kernel", kernel: "6.6.0-hiveos", want: true},
		{name: "generic Ubuntu kernel", kernel: "6.8.0-52-generic", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isHiveOSKernel(test.kernel); got != test.want {
				t.Fatalf("isHiveOSKernel(%q) = %v, want %v", test.kernel, got, test.want)
			}
		})
	}
}

func TestValidateDriverSelectionForEachVendor(t *testing.T) {
	tests := []protocol.DriverUpdateRequest{
		{Vendor: "NVIDIA", Package: "nvidia-driver-550", Version: "550.127.05-0ubuntu0.24.04.1"},
		{Vendor: "NVIDIA", Package: hiveOSNVIDIAPackage, Version: "550.127.05"},
		{Vendor: "AMD", Package: "amdgpu-dkms", Version: "6.4.60400-1"},
		{Vendor: "INTEL", Package: "intel-level-zero-gpu", Version: "1.6.32224.5-1"},
	}
	for _, request := range tests {
		if err := validateDriverSelection(request); err != nil {
			t.Errorf("validateDriverSelection(%#v) error = %v", request, err)
		}
	}
}

func TestValidateDriverSelectionRejectsMismatchedOrUnsafeValues(t *testing.T) {
	tests := []protocol.DriverUpdateRequest{
		{Vendor: "AMD", Package: "nvidia-driver-550", Version: "550.127.05"},
		{Vendor: "INTEL", Package: "intel-level-zero-gpu", Version: "-oAPT::Update::Pre-Invoke=x"},
		{Vendor: "NVIDIA", Package: "nvidia-driver-550", Version: ""},
	}
	for _, request := range tests {
		if err := validateDriverSelection(request); err == nil {
			t.Errorf("validateDriverSelection(%#v) accepted invalid selection", request)
		}
	}
}

func TestFormatHiveOSNVIDIAVersions(t *testing.T) {
	output, err := formatHiveOSNVIDIAVersions("\x1b[32m550.127.05\x1b[0m\n535.183.01\n550.127.05\n")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	want := []string{
		hiveOSNVIDIAPackage + "|550.127.05",
		hiveOSNVIDIAPackage + "|535.183.01",
	}
	if len(lines) != len(want) {
		t.Fatalf("formatHiveOSNVIDIAVersions() = %q, want %q", lines, want)
	}
	for index := range want {
		if lines[index] != want[index] {
			t.Fatalf("formatHiveOSNVIDIAVersions()[%d] = %q, want %q", index, lines[index], want[index])
		}
	}
}

func TestFormatAPTDriverPackages(t *testing.T) {
	output := formatAPTDriverPackages([]byte(
		" nvidia-driver-550 | 550.127.05-0ubuntu0.24.04.1 | http://archive.ubuntu.com noble/restricted amd64 Packages\n" +
			" linux-firmware | 20240318.git3b128b60-0ubuntu2.18 | http://archive.ubuntu.com noble-updates/main amd64 Packages\n",
	))
	want := "nvidia-driver-550|550.127.05-0ubuntu0.24.04.1\nlinux-firmware|20240318.git3b128b60-0ubuntu2.18\n"
	if output != want {
		t.Fatalf("formatAPTDriverPackages() = %q, want %q", output, want)
	}
}

func TestParseNVIDIAPackageNames(t *testing.T) {
	packages := parseNVIDIAPackageNames([]byte(
		"nvidia-driver-550\nnvidia-driver-535-server\nnvidia-driver-535\nnvidia-driver-550\nlibnvidia-driver-550\n",
	))
	want := []string{"nvidia-driver-550", "nvidia-driver-535"}
	if strings.Join(packages, ",") != strings.Join(want, ",") {
		t.Fatalf("parseNVIDIAPackageNames() = %q, want %q", packages, want)
	}
}

func TestSelectedDriverInstallCommandUsesExactVersion(t *testing.T) {
	tests := []struct {
		name        string
		request     protocol.DriverUpdateRequest
		hiveOS      bool
		wantCommand string
		wantArgs    []string
	}{
		{
			name:        "HiveOS NVIDIA",
			request:     protocol.DriverUpdateRequest{Vendor: "NVIDIA", Package: hiveOSNVIDIAPackage, Version: "550.127.05"},
			hiveOS:      true,
			wantCommand: "nvidia-driver-update",
			wantArgs:    []string{"550.127.05"},
		},
		{
			name:        "Ubuntu NVIDIA",
			request:     protocol.DriverUpdateRequest{Vendor: "NVIDIA", Package: "nvidia-driver-550", Version: "550.127.05-0ubuntu0.24.04.1"},
			wantCommand: "/usr/bin/apt-get",
			wantArgs:    []string{"install", "-y", "--", "nvidia-driver-550=550.127.05-0ubuntu0.24.04.1"},
		},
		{
			name:        "AMD",
			request:     protocol.DriverUpdateRequest{Vendor: "AMD", Package: "amdgpu-dkms", Version: "6.4.60400-1"},
			wantCommand: "/usr/bin/apt-get",
			wantArgs:    []string{"install", "-y", "--", "amdgpu-dkms=6.4.60400-1"},
		},
		{
			name:        "Intel",
			request:     protocol.DriverUpdateRequest{Vendor: "INTEL", Package: "intel-level-zero-gpu", Version: "1.6.32224.5-1"},
			wantCommand: "/usr/bin/apt-get",
			wantArgs:    []string{"install", "-y", "--", "intel-level-zero-gpu=1.6.32224.5-1"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, arguments, err := selectedDriverInstallCommand(test.request, test.hiveOS)
			if err != nil {
				t.Fatal(err)
			}
			if command != test.wantCommand {
				t.Fatalf("command = %q, want %q", command, test.wantCommand)
			}
			if strings.Join(arguments, "\x00") != strings.Join(test.wantArgs, "\x00") {
				t.Fatalf("arguments = %q, want %q", arguments, test.wantArgs)
			}
		})
	}
}
