package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCPUTemperatureUsesHottestSupportedSensor(t *testing.T) {
	root := t.TempDir()
	writeSensor := func(directory, name, value string) {
		path := filepath.Join(root, directory)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "name"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "temp1_input"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSensor("hwmon0", "k10temp", "65625")
	writeSensor("hwmon1", "coretemp", "71250")
	writeSensor("hwmon2", "nvme", "90000")

	if temperature := cpuTemperatureAt(root); temperature != 71.25 {
		t.Fatalf("CPU temperature = %v, want 71.25", temperature)
	}
}

func TestParseNVIDIAGPUsIncludesDriverFirmwarePCIAddressAndPowerLimit(t *testing.T) {
	output := []byte("0, NVIDIA RTX 3070, GPU-123, 00000000:01:00.0, 550.54.14, 94.04.3A.00.12, 62, 118.5, 115, 70, 99, 4096, 8192, 1455, 7000\n")
	gpus := parseNVIDIAGPUs(output)
	if len(gpus) != 1 {
		t.Fatalf("GPU count = %d, want 1", len(gpus))
	}
	gpu := gpus[0]
	if gpu.Driver != "550.54.14" || gpu.Firmware != "94.04.3A.00.12" || gpu.PCIAddress != "00000000:01:00.0" {
		t.Fatalf("GPU software identity = %#v", gpu)
	}
	if gpu.MemoryUsed != 4096*1024*1024 || gpu.CoreClock != 1455 || gpu.PowerLimit != 115 {
		t.Fatalf("GPU metrics = %#v", gpu)
	}
}

func TestAMDGPUsAtIncludesDriverFirmwareAndPCIAddress(t *testing.T) {
	root := t.TempDir()
	device := filepath.Join(root, "card2", "device")
	hwmon := filepath.Join(device, "hwmon", "hwmon0")
	if err := os.MkdirAll(hwmon, 0o700); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"vendor": "0x1002", "product_name": "Radeon RX 6800", "vbios_version": "020.001.000.060",
		"uevent": "DRIVER=amdgpu\nPCI_SLOT_NAME=0000:0b:00.0\n", "gpu_busy_percent": "97",
		"mem_info_vram_used": "1024", "mem_info_vram_total": "2048",
	}
	for name, value := range values {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(hwmon, "temp1_input"), []byte("65000"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmon, "power1_cap"), []byte("115000000"), 0o600); err != nil {
		t.Fatal(err)
	}
	gpus := amdGPUsAt(root, "6.8.5")
	if len(gpus) != 1 {
		t.Fatalf("GPU count = %d, want 1", len(gpus))
	}
	gpu := gpus[0]
	if gpu.Driver != "6.8.5" || gpu.Firmware != "020.001.000.060" || gpu.PCIAddress != "0000:0b:00.0" {
		t.Fatalf("GPU software identity = %#v", gpu)
	}
	if gpu.PowerLimit != 115 {
		t.Fatalf("GPU power limit = %v, want 115 W", gpu.PowerLimit)
	}
}

func TestIntelGPUsAtIncludesDriverFirmwareAndPCIAddress(t *testing.T) {
	root := t.TempDir()
	device := filepath.Join(root, "card1", "device")
	hwmon := filepath.Join(device, "hwmon", "hwmon0")
	if err := os.MkdirAll(hwmon, 0o700); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"vendor": "0x8086", "product_name": "Intel Arc A770", "firmware_version": "GuC 70.36.0",
		"uevent": "DRIVER=xe\nPCI_SLOT_NAME=0000:03:00.0\n", "gpu_busy_percent": "83",
	}
	for name, value := range values {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(hwmon, "temp1_input"), []byte("61000"), 0o600); err != nil {
		t.Fatal(err)
	}
	gpus := intelGPUsAt(root, "6.11.0")
	if len(gpus) != 1 {
		t.Fatalf("GPU count = %d, want 1", len(gpus))
	}
	gpu := gpus[0]
	if gpu.Vendor != "INTEL" || gpu.Name != "Intel Arc A770" || gpu.Driver != "6.11.0" ||
		gpu.Firmware != "GuC 70.36.0" || gpu.PCIAddress != "0000:03:00.0" {
		t.Fatalf("GPU software identity = %#v", gpu)
	}
	if gpu.Temperature != 61 || gpu.Utilization != 83 {
		t.Fatalf("GPU metrics = %#v", gpu)
	}
}
