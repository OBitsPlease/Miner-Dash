package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

func TestNVIDIASettingCommandMapping(t *testing.T) {
	setting := protocol.GPUOverclock{
		PowerLimitW: 110, CoreClockMHz: 1410, MemoryClockMHz: 5001,
		CoreOffsetMHz: -75, MemoryOffsetMHz: 125,
	}
	wantSMI := [][]string{{"-pl", "110"}, {"-lgc", "1410,1410"}, {"-lmc", "5001,5001"}}
	if got := nvidiaSMISettingCommands(setting); !reflect.DeepEqual(got, wantSMI) {
		t.Fatalf("nvidia-smi commands = %#v, want %#v", got, wantSMI)
	}
	wantSettings := []string{
		"-a", "[gpu:3]/GPUGraphicsClockOffsetAllPerformanceLevels=-75",
		"-a", "[gpu:3]/GPUMemoryTransferRateOffsetAllPerformanceLevels=125",
	}
	if got := nvidiaSettingsOffsetArguments(3, setting); !reflect.DeepEqual(got, wantSettings) {
		t.Fatalf("nvidia-settings arguments = %#v, want %#v", got, wantSettings)
	}
}

func TestTransitionOverclockSkipsIdenticalProfile(t *testing.T) {
	profile := protocol.OverclockProfile{
		ID: "profile", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{{Selector: "0", PowerLimitW: 115}},
	}
	if err := transitionOverclock(profile, profile); err != nil {
		t.Fatalf("identical profile transition failed: %v", err)
	}
}

func TestRemovedOverclockSettingsOnlyResetsClearedControls(t *testing.T) {
	previous := protocol.OverclockProfile{
		ID: "profile", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{{
			Selector: "0", PowerLimitW: 115, FanPercent: 70,
			CoreClockMHz: 1410, MemoryClockMHz: 5001,
		}},
	}
	next := protocol.OverclockProfile{
		ID: "profile", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{{
			Selector: "0", PowerLimitW: 110, CoreClockMHz: 1380,
		}},
	}
	got := removedOverclockSettings(previous, next)
	want := []protocol.GPUOverclock{{Selector: "0", FanPercent: 70, MemoryClockMHz: 5001}}
	if !reflect.DeepEqual(got.Settings, want) {
		t.Fatalf("removed settings = %#v, want %#v", got.Settings, want)
	}
}

func TestApplyGPUPowerPolicyClampsProfileAndAddsWildcardForUnassignedGPUs(t *testing.T) {
	profile := protocol.OverclockProfile{
		ID: "profile", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{
			{Selector: "0", PowerLimitW: 180},
			{Selector: "1", PowerLimitW: 100},
		},
	}
	gpus := []protocol.GPU{{Index: 0, Vendor: "NVIDIA"}, {Index: 1, Vendor: "NVIDIA"}, {Index: 2, Vendor: "NVIDIA"}}
	got := applyGPUPowerPolicyToProfile(profile, 115, gpus)
	want := []protocol.GPUOverclock{
		{Selector: "*", PowerLimitW: 115},
		{Selector: "0", PowerLimitW: 115},
		{Selector: "1", PowerLimitW: 100},
	}
	if !reflect.DeepEqual(got.Settings, want) {
		t.Fatalf("profile settings = %#v, want %#v", got.Settings, want)
	}
	if got := removedOverclockSettings(profile, got); len(got.Settings) != 0 {
		t.Fatalf("settings removed while policy cap remains = %#v, want none", got.Settings)
	}
}

func TestApplyGPUPowerPolicyCreatesProfileWhenNoProfileAssigned(t *testing.T) {
	gpus := []protocol.GPU{{Index: 0, Vendor: "NVIDIA"}, {Index: 1, Vendor: "NVIDIA"}}
	got := applyGPUPowerPolicyToProfile(protocol.OverclockProfile{}, 115, gpus)
	if got.ID == "" || got.Vendor != "NVIDIA" || !reflect.DeepEqual(got.Settings, []protocol.GPUOverclock{{Selector: "*", PowerLimitW: 115}}) {
		t.Fatalf("generated policy profile = %#v", got)
	}
}

func TestPreflightNVIDIASettingsDoesNotRequireDisplayForPowerLimit(t *testing.T) {
	err := preflightNVIDIASettings("unused", []protocol.GPUOverclock{{Selector: "0", PowerLimitW: 115}})
	if err != nil {
		t.Fatalf("power-only preflight failed: %v", err)
	}
}

func TestPreflightNVIDIASettingsRejectsUnavailableFanControlBeforeTuning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses POSIX executable scripts")
	}
	directory := t.TempDir()
	smi := filepath.Join(directory, "nvidia-smi")
	settings := filepath.Join(directory, "nvidia-settings")
	if err := os.WriteFile(smi, []byte("#!/bin/sh\nprintf '0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("#!/bin/sh\necho 'ERROR: The control display is undefined' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := preflightNVIDIASettings(smi, []protocol.GPUOverclock{{Selector: "0", PowerLimitW: 115, FanPercent: 80}})
	if err == nil {
		t.Fatal("expected unavailable fan control to fail preflight")
	}
	if !strings.Contains(err.Error(), "no tuning changes were applied") || !strings.Contains(err.Error(), "control display is undefined") {
		t.Fatalf("unexpected error: %v", err)
	}
}
