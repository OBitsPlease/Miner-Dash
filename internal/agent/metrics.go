package agent

import (
	"bufio"
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"minerdash/internal/protocol"
)

type MetricsCollector struct {
	mu        sync.Mutex
	lastIdle  uint64
	lastTotal uint64
}

func (c *MetricsCollector) Collect(miner protocol.MinerState) protocol.Metrics {
	metrics := protocol.Metrics{CollectedAt: time.Now().UTC(), Miner: miner, GPUs: []protocol.GPU{}, System: systemInfo()}
	metrics.CPUUsage = c.cpuUsage()
	metrics.CPUTemperature = cpuTemperatureAt("/sys/class/hwmon")
	metrics.Load1 = readFloatField("/proc/loadavg", 0)
	metrics.Uptime = uint64(readFloatField("/proc/uptime", 0))
	metrics.MemoryTotal, metrics.MemoryUsed = memory()
	metrics.GPUs = append(metrics.GPUs, nvidiaGPUs()...)
	metrics.GPUs = append(metrics.GPUs, amdGPUs()...)
	return metrics
}

func cpuTemperatureAt(root string) float64 {
	directories, _ := filepath.Glob(filepath.Join(root, "hwmon*"))
	var hottest float64
	for _, directory := range directories {
		switch readTrimmed(filepath.Join(directory, "name")) {
		case "coretemp", "k10temp", "zenpower":
		default:
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(directory, "temp*_input"))
		for _, input := range inputs {
			temperature := readFloat(input) / 1000
			if temperature > hottest && temperature < 150 {
				hottest = temperature
			}
		}
	}
	return hottest
}

func (c *MetricsCollector) cpuUsage() float64 {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 8 || fields[0] != "cpu" {
		return 0
	}
	var values []uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0
		}
		values = append(values, value)
	}
	idle := values[3] + values[4]
	var total uint64
	for _, value := range values {
		total += value
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	deltaTotal, deltaIdle := total-c.lastTotal, idle-c.lastIdle
	c.lastTotal, c.lastIdle = total, idle
	if deltaTotal == 0 {
		return 0
	}
	return 100 * float64(deltaTotal-deltaIdle) / float64(deltaTotal)
}

func memory() (total, used uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var available uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = value * 1024
		case "MemAvailable:":
			available = value * 1024
		}
	}
	return total, total - available
}

func nvidiaGPUs() []protocol.GPU {
	command := exec.Command("nvidia-smi", "--query-gpu=index,name,uuid,pci.bus_id,driver_version,vbios_version,temperature.gpu,power.draw,fan.speed,utilization.gpu,memory.used,memory.total,clocks.current.graphics,clocks.current.memory", "--format=csv,noheader,nounits")
	output, err := command.Output()
	if err != nil {
		return nil
	}
	return parseNVIDIAGPUs(output)
}

func parseNVIDIAGPUs(output []byte) []protocol.GPU {
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		return nil
	}
	result := make([]protocol.GPU, 0, len(rows))
	for _, row := range rows {
		if len(row) != 14 {
			continue
		}
		index, _ := strconv.Atoi(strings.TrimSpace(row[0]))
		memoryUsedMiB, _ := strconv.ParseUint(strings.TrimSpace(row[10]), 10, 64)
		memoryTotalMiB, _ := strconv.ParseUint(strings.TrimSpace(row[11]), 10, 64)
		result = append(result, protocol.GPU{
			Index: index, Vendor: "NVIDIA", Name: strings.TrimSpace(row[1]),
			UUID: strings.TrimSpace(row[2]), PCIAddress: strings.TrimSpace(row[3]),
			Driver: strings.TrimSpace(row[4]), Firmware: strings.TrimSpace(row[5]),
			Temperature: parseFloat(row[6]), Power: parseFloat(row[7]), Fan: parseFloat(row[8]),
			Utilization: parseFloat(row[9]), MemoryUsed: memoryUsedMiB * 1024 * 1024,
			MemoryTotal: memoryTotalMiB * 1024 * 1024, CoreClock: parseFloat(row[12]), MemoryClock: parseFloat(row[13]),
		})
	}
	return result
}

func amdGPUs() []protocol.GPU {
	return amdGPUsAt("/sys/class/drm", amdDriverVersion())
}

func amdGPUsAt(root, driverVersion string) []protocol.GPU {
	cards, _ := filepath.Glob(filepath.Join(root, "card[0-9]*", "device"))
	var result []protocol.GPU
	for _, device := range cards {
		vendor, err := os.ReadFile(filepath.Join(device, "vendor"))
		if err != nil || strings.TrimSpace(string(vendor)) != "0x1002" {
			continue
		}
		indexText := strings.TrimPrefix(filepath.Base(filepath.Dir(device)), "card")
		index, _ := strconv.Atoi(indexText)
		name := readTrimmed(filepath.Join(device, "product_name"))
		if name == "" {
			name = readTrimmed(filepath.Join(device, "device"))
		}
		gpu := protocol.GPU{
			Index: index, Vendor: "AMD", Name: name, Driver: driverVersion,
			PCIAddress: pciAddress(device),
			Firmware: firstNonEmpty(
				readTrimmed(filepath.Join(device, "vbios_version")),
				readTrimmed(filepath.Join(device, "firmware_version")),
			),
			Utilization: readFloat(filepath.Join(device, "gpu_busy_percent")),
			MemoryUsed:  uint64(readFloat(filepath.Join(device, "mem_info_vram_used"))),
			MemoryTotal: uint64(readFloat(filepath.Join(device, "mem_info_vram_total"))),
		}
		hwmons, _ := filepath.Glob(filepath.Join(device, "hwmon", "hwmon*"))
		if len(hwmons) > 0 {
			gpu.Temperature = readFloat(filepath.Join(hwmons[0], "temp1_input")) / 1000
			gpu.Power = readFloat(filepath.Join(hwmons[0], "power1_average")) / 1_000_000
		}
		result = append(result, gpu)
	}
	return result
}

func amdDriverVersion() string {
	if version := readTrimmed("/sys/module/amdgpu/version"); version != "" {
		return version
	}
	if version := commandOutput("modinfo", "-F", "version", "amdgpu"); version != "" {
		return version
	}
	if kernel := commandOutput("uname", "-r"); kernel != "" {
		return "kernel " + kernel
	}
	return ""
}

func pciAddress(device string) string {
	for _, line := range strings.Split(readTrimmed(filepath.Join(device, "uevent")), "\n") {
		if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
			return strings.TrimPrefix(line, "PCI_SLOT_NAME=")
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func systemInfo() protocol.SystemInfo {
	hostname, _ := os.Hostname()
	info := protocol.SystemInfo{
		Hostname: hostname, CPUCores: runtime.NumCPU(), Agent: "0.1.0",
		Kernel: commandOutput("uname", "-r"),
	}
	for _, line := range strings.Split(readTrimmed("/etc/os-release"), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			break
		}
	}
	for _, line := range strings.Split(readTrimmed("/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(line, "model name") {
			_, value, ok := strings.Cut(line, ":")
			if ok {
				info.CPUModel = strings.TrimSpace(value)
			}
			break
		}
	}
	return info
}

func commandOutput(name string, arguments ...string) string {
	output, err := exec.Command(name, arguments...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func readFloatField(path string, index int) float64 {
	fields := strings.Fields(readTrimmed(path))
	if index >= len(fields) {
		return 0
	}
	return parseFloat(fields[index])
}

func readFloat(path string) float64 {
	return parseFloat(readTrimmed(path))
}

func parseFloat(value string) float64 {
	result, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return result
}

func readTrimmed(path string) string {
	value, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}
