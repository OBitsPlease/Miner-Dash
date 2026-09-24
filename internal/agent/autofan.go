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
	for _, gpu := range gpus {
		percent := desiredFanPercent(policy, gpu.Temperature)
		key := fmt.Sprintf("%s:%d", gpu.Vendor, gpu.Index)
		if old, ok := previous[key]; ok && abs(old-percent) < 3 {
			continue
		}
		switch gpu.Vendor {
		case "NVIDIA":
			if err := setNVIDIAFan(gpu.Index, percent); err != nil {
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
	path, err := exec.LookPath("nvidia-settings")
	if err != nil {
		return errors.New("nvidia-settings is required for NVIDIA autofan")
	}
	arguments := []string{
		"-a", fmt.Sprintf("[gpu:%d]/GPUFanControlState=1", index),
		"-a", fmt.Sprintf("[fan:%d]/GPUTargetFanSpeed=%d", index, percent),
	}
	if output, err := exec.Command(path, arguments...).CombinedOutput(); err != nil {
		return fmt.Errorf("set NVIDIA fan: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
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
