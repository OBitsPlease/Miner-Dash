package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"minerdash/internal/protocol"
)

func applyAutofan(policy protocol.AutofanPolicy, gpus []protocol.GPU, previous map[string]int) error {
	if !policy.Enabled {
		return nil
	}
	if runtime.GOOS != "linux" {
		return errors.New("autofan is supported only on Linux agents")
	}
	var nvidiaSettingsPath string
	var nvidiaFans map[int][]int
	for _, gpu := range gpus {
		percent := desiredFanPercent(policy, gpu.Temperature)
		key := fmt.Sprintf("%s:%d", gpu.Vendor, gpu.Index)
		if old, ok := previous[key]; ok && abs(old-percent) < 3 {
			continue
		}
		switch gpu.Vendor {
		case "NVIDIA":
			if nvidiaFans == nil {
				var err error
				nvidiaSettingsPath, err = exec.LookPath("nvidia-settings")
				if err != nil {
					return errors.New("nvidia-settings is required for NVIDIA autofan")
				}
				smiPath, err := exec.LookPath("nvidia-smi")
				if err != nil {
					return errors.New("nvidia-smi is required to map NVIDIA fans to GPUs")
				}
				nvidiaFans, err = nvidiaFansByGPU(smiPath, nvidiaSettingsPath)
				if err != nil {
					return err
				}
			}
			if err := setNVIDIAFanTargets(nvidiaSettingsPath, gpu.Index, percent, nvidiaFans[gpu.Index]); err != nil {
				return err
			}
		case "AMD":
			if err := setAMDFan(gpu.Index, percent); err != nil {
				return err
			}
		default:
			continue
		}
		previous[key] = percent
	}
	return nil
}

func resetAutofan() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if path, err := exec.LookPath("nvidia-smi"); err == nil {
		if indices, err := selectedNVIDIAIndices(path, "*"); err == nil {
			if err := resetNVIDIAFans(indices); err != nil {
				return err
			}
		}
	}
	cards, _ := filepath.Glob("/sys/class/drm/card[0-9]*")
	for _, card := range cards {
		if !isAMDCard(card) {
			continue
		}
		hwmons, _ := filepath.Glob(filepath.Join(card, "device", "hwmon", "hwmon*"))
		if len(hwmons) > 0 {
			if err := os.WriteFile(filepath.Join(hwmons[0], "pwm1_enable"), []byte("2"), 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func desiredFanPercent(policy protocol.AutofanPolicy, temperature float64) int {
	percent := policy.MinimumFanPercent + int((temperature-policy.TargetTemperatureC)*5)
	if percent < policy.MinimumFanPercent {
		percent = policy.MinimumFanPercent
	}
	if percent > policy.MaximumFanPercent {
		percent = policy.MaximumFanPercent
	}
	return percent
}

func setNVIDIAFan(index, percent int) error {
	settingsPath, err := exec.LookPath("nvidia-settings")
	if err != nil {
		return errors.New("nvidia-settings is required for NVIDIA autofan")
	}
	smiPath, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return errors.New("nvidia-smi is required to map NVIDIA fans to GPUs")
	}
	fansByGPU, err := nvidiaFansByGPU(smiPath, settingsPath)
	if err != nil {
		return err
	}
	return setNVIDIAFanTargets(settingsPath, index, percent, fansByGPU[index])
}

func setNVIDIAFanTargets(settingsPath string, index, percent int, fans []int) error {
	if len(fans) == 0 {
		return fmt.Errorf("NVIDIA GPU %d has no mapped fans", index)
	}
	arguments := []string{"-a", fmt.Sprintf("[gpu:%d]/GPUFanControlState=1", index)}
	for _, fan := range fans {
		arguments = append(arguments, "-a", fmt.Sprintf("[fan:%d]/GPUTargetFanSpeed=%d", fan, percent))
	}
	if output, err := exec.Command(settingsPath, arguments...).CombinedOutput(); err != nil {
		return fmt.Errorf("set NVIDIA fan: %s: %w", strings.TrimSpace(string(output)), err)
	}
	for _, fan := range fans {
		output, err := exec.Command(settingsPath, "-t", "-q", fmt.Sprintf("[fan:%d]/GPUTargetFanSpeed", fan)).Output()
		if err != nil {
			return fmt.Errorf("read back NVIDIA fan %d target: %w", fan, err)
		}
		actual, err := strconv.Atoi(strings.TrimSpace(string(output)))
		if err != nil {
			return fmt.Errorf("parse NVIDIA fan %d target %q: %w", fan, strings.TrimSpace(string(output)), err)
		}
		if actual != percent {
			return fmt.Errorf("NVIDIA fan %d target is %d%% after requesting %d%%", fan, actual, percent)
		}
	}
	return nil
}

func nvidiaFansByGPU(smiPath, settingsPath string) (map[int][]int, error) {
	gpuIndices, err := selectedNVIDIAIndices(smiPath, "*")
	if err != nil {
		return nil, err
	}
	output, err := exec.Command(settingsPath, "-q", "fans", "--terse").Output()
	if err != nil {
		return nil, fmt.Errorf("list NVIDIA fans: %w", err)
	}
	fanCount := 0
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[1], "fans") {
			continue
		}
		fanCount, err = strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("parse NVIDIA fan count %q: %w", fields[0], err)
		}
		break
	}
	return mapNVIDIAFansToGPUs(gpuIndices, fanCount)
}

func mapNVIDIAFansToGPUs(gpuIndices []int, fanCount int) (map[int][]int, error) {
	if len(gpuIndices) == 0 || fanCount == 0 {
		return nil, fmt.Errorf("no NVIDIA fan targets found for %d GPUs", len(gpuIndices))
	}
	allFans := make([]int, fanCount)
	for fan := range allFans {
		allFans[fan] = fan
	}
	fansByGPU := make(map[int][]int, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		fansByGPU[gpuIndex] = append([]int(nil), allFans...)
	}
	return fansByGPU, nil
}

func resetNVIDIAFans(indices []int) error {
	path, err := exec.LookPath("nvidia-settings")
	if err != nil {
		return nil
	}
	for _, index := range indices {
		if output, err := exec.Command(path, "-a", fmt.Sprintf("[gpu:%d]/GPUFanControlState=0", index)).CombinedOutput(); err != nil {
			return fmt.Errorf("reset NVIDIA fan: %s: %w", strings.TrimSpace(string(output)), err)
		}
	}
	return nil
}

func setAMDFan(index, percent int) error {
	card := filepath.Join("/sys/class/drm", "card"+strconv.Itoa(index))
	if !isAMDCard(card) {
		return fmt.Errorf("AMD GPU %d was not found", index)
	}
	hwmons, _ := filepath.Glob(filepath.Join(card, "device", "hwmon", "hwmon*"))
	if len(hwmons) == 0 {
		return fmt.Errorf("no hwmon controls found for AMD GPU %d", index)
	}
	if err := os.WriteFile(filepath.Join(hwmons[0], "pwm1_enable"), []byte("1"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(hwmons[0], "pwm1"), []byte(strconv.Itoa(percent*255/100)), 0o600)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
