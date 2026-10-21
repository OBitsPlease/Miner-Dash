package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"minerdash/internal/protocol"
)

func applyOverclock(profile protocol.OverclockProfile) error {
	if profile.ID == "" {
		return nil
	}
	if runtime.GOOS != "linux" {
		return errors.New("overclocking is supported only on Linux agents")
	}
	switch profile.Vendor {
	case "NVIDIA":
		return applyNVIDIAOverclock(profile.Settings)
	case "AMD":
		return applyAMDOverclock(profile.Settings)
	default:
		return fmt.Errorf("unsupported overclock vendor %q", profile.Vendor)
	}
}

func transitionOverclock(previous, next protocol.OverclockProfile) error {
	if reflect.DeepEqual(previous, next) {
		return nil
	}
	if err := preflightOverclock(next); err != nil {
		return err
	}
	removed := removedOverclockSettings(previous, next)
	if err := resetOverclock(removed); err != nil {
		return fmt.Errorf("reset removed overclock settings: %w", err)
	}
	if err := applyOverclock(next); err != nil {
		if rollbackErr := applyOverclock(previous); rollbackErr != nil {
			return fmt.Errorf("apply overclock: %v; restore previous overclock: %w", err, rollbackErr)
		}
		return fmt.Errorf("apply overclock; previous overclock restored: %w", err)
	}
	return nil
}

func enforceGPUPowerLimit(maximum int, gpus []protocol.GPU) error {
	if maximum <= 0 {
		return nil
	}
	for _, gpu := range gpus {
		if gpu.PowerLimit > 0 && gpu.PowerLimit <= float64(maximum) {
			continue
		}
		switch strings.ToUpper(gpu.Vendor) {
		case "NVIDIA":
			if err := setNVIDIAPowerLimit(gpu.Index, maximum); err != nil {
				return fmt.Errorf("enforce %d W limit on NVIDIA GPU %d: %w", maximum, gpu.Index, err)
			}
		case "AMD":
			if err := applyAMDOverclock([]protocol.GPUOverclock{{
				Selector: strconv.Itoa(gpu.Index), PowerLimitW: maximum,
			}}); err != nil {
				return fmt.Errorf("enforce %d W limit on AMD GPU %d: %w", maximum, gpu.Index, err)
			}
		default:
			return fmt.Errorf("cannot enforce GPU power limit for unsupported vendor %q", gpu.Vendor)
		}
	}
	return nil
}

func applyGPUPowerPolicyToProfile(profile protocol.OverclockProfile, maximum int, gpus []protocol.GPU) protocol.OverclockProfile {
	if maximum <= 0 || len(gpus) == 0 {
		return profile
	}
	if profile.ID == "" {
		profile.ID = "gpu-power-policy"
		profile.Name = "Controller GPU power policy"
		profile.Vendor = gpus[0].Vendor
	}
	if profile.Vendor == "" {
		profile.Vendor = gpus[0].Vendor
	}
	settings := make([]protocol.GPUOverclock, 0, len(profile.Settings)+1)
	hasWildcard := false
	for _, setting := range profile.Settings {
		if setting.Selector == "*" {
			hasWildcard = true
		}
		if setting.PowerLimitW == 0 || setting.PowerLimitW > maximum {
			setting.PowerLimitW = maximum
		}
		settings = append(settings, setting)
	}
	if !hasWildcard {
		settings = append([]protocol.GPUOverclock{{Selector: "*", PowerLimitW: maximum}}, settings...)
	}
	profile.Settings = settings
	return profile
}

func removedOverclockSettings(previous, next protocol.OverclockProfile) protocol.OverclockProfile {
	removed := protocol.OverclockProfile{ID: previous.ID, Vendor: previous.Vendor}
	for _, old := range previous.Settings {
		var replacement protocol.GPUOverclock
		for _, candidate := range next.Settings {
			if candidate.Selector == old.Selector {
				replacement = candidate
				break
			}
			if candidate.Selector == "*" && candidate.PowerLimitW > 0 {
				replacement.PowerLimitW = candidate.PowerLimitW
			}
		}
		setting := protocol.GPUOverclock{Selector: old.Selector}
		if old.CoreClockMHz != 0 && replacement.CoreClockMHz == 0 {
			setting.CoreClockMHz = old.CoreClockMHz
		}
		if old.MemoryClockMHz != 0 && replacement.MemoryClockMHz == 0 {
			setting.MemoryClockMHz = old.MemoryClockMHz
		}
		if old.CoreOffsetMHz != 0 && replacement.CoreOffsetMHz == 0 {
			setting.CoreOffsetMHz = old.CoreOffsetMHz
		}
		if old.MemoryOffsetMHz != 0 && replacement.MemoryOffsetMHz == 0 {
			setting.MemoryOffsetMHz = old.MemoryOffsetMHz
		}
		if old.PowerLimitW != 0 && replacement.PowerLimitW == 0 {
			setting.PowerLimitW = old.PowerLimitW
		}
		if old.FanPercent != 0 && replacement.FanPercent == 0 {
			setting.FanPercent = old.FanPercent
		}
		if setting != (protocol.GPUOverclock{Selector: old.Selector}) {
			removed.Settings = append(removed.Settings, setting)
		}
	}
	if len(removed.Settings) == 0 {
		removed.ID = ""
	}
	return removed
}

func preflightOverclock(profile protocol.OverclockProfile) error {
	if profile.ID == "" {
		return nil
	}
	if runtime.GOOS != "linux" {
		return errors.New("overclocking is supported only on Linux agents")
	}
	if profile.Vendor != "NVIDIA" {
		return nil
	}
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return errors.New("nvidia-smi is required to apply NVIDIA overclocks")
	}
	return preflightNVIDIASettings(path, profile.Settings)
}

func resetOverclock(profile protocol.OverclockProfile) error {
	if profile.ID == "" || runtime.GOOS != "linux" {
		return nil
	}
	switch profile.Vendor {
	case "NVIDIA":
		path, err := exec.LookPath("nvidia-smi")
		if err != nil {
			return err
		}
		var fanIndices []int
		for _, setting := range profile.Settings {
			indices, err := selectedNVIDIAIndices(path, setting.Selector)
			if err != nil {
				return err
			}
			for _, index := range indices {
				if setting.CoreClockMHz != 0 {
					if err := runNVIDIACommand(path, index, "-rgc"); err != nil {
						return err
					}
				}
				if setting.MemoryClockMHz != 0 {
					if err := runNVIDIACommand(path, index, "-rmc"); err != nil {
						return err
					}
				}
				if setting.PowerLimitW != 0 {
					defaultPower, err := nvidiaDefaultPower(path, index)
					if err != nil {
						return err
					}
					if err := runNVIDIACommand(path, index, "-pl", strconv.Itoa(defaultPower)); err != nil {
						return err
					}
				}
				if setting.CoreOffsetMHz != 0 || setting.MemoryOffsetMHz != 0 {
					settingsPath, err := exec.LookPath("nvidia-settings")
					if err != nil {
						return err
					}
					arguments := []string{}
					if setting.CoreOffsetMHz != 0 {
						arguments = append(arguments, "-a", fmt.Sprintf("[gpu:%d]/GPUGraphicsClockOffsetAllPerformanceLevels=0", index))
					}
					if setting.MemoryOffsetMHz != 0 {
						arguments = append(arguments, "-a", fmt.Sprintf("[gpu:%d]/GPUMemoryTransferRateOffsetAllPerformanceLevels=0", index))
					}
					if output, err := exec.Command(settingsPath, arguments...).CombinedOutput(); err != nil {
						return fmt.Errorf("reset NVIDIA clock offset: %s: %w", strings.TrimSpace(string(output)), err)
					}
				}
				if setting.FanPercent != 0 {
					fanIndices = append(fanIndices, index)
				}
			}
		}
		return resetNVIDIAFans(fanIndices)
	case "AMD":
		for _, setting := range profile.Settings {
			cards, err := selectedAMDCards(setting.Selector)
			if err != nil {
				return err
			}
			for _, card := range cards {
				hwmons, _ := filepath.Glob(filepath.Join(card, "device", "hwmon", "hwmon*"))
				if len(hwmons) == 0 {
					continue
				}
				if setting.PowerLimitW != 0 {
					defaultPower := readTrimmed(filepath.Join(hwmons[0], "power1_cap_default"))
					if defaultPower != "" {
						if err := os.WriteFile(filepath.Join(hwmons[0], "power1_cap"), []byte(defaultPower), 0o600); err != nil {
							return err
						}
					}
				}
				if setting.FanPercent != 0 {
					if err := os.WriteFile(filepath.Join(hwmons[0], "pwm1_enable"), []byte("2"), 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
						return err
					}
				}
			}
		}
		return nil
	default:
		return nil
	}
}

func applyNVIDIAOverclock(settings []protocol.GPUOverclock) error {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return errors.New("nvidia-smi is required to apply NVIDIA overclocks")
	}
	if err := preflightNVIDIASettings(path, settings); err != nil {
		return err
	}
	settingsPath := ""
	var fansByGPU map[int][]int
	for _, setting := range settings {
		if setting.FanPercent == 0 {
			continue
		}
		settingsPath, err = exec.LookPath("nvidia-settings")
		if err != nil {
			return errors.New("nvidia-settings is required for NVIDIA fans")
		}
		fansByGPU, err = nvidiaFansByGPU(path, settingsPath)
		if err != nil {
			return err
		}
		break
	}
	for _, setting := range settings {
		indices, err := selectedNVIDIAIndices(path, setting.Selector)
		if err != nil {
			return err
		}
		for _, index := range indices {
			for _, arguments := range nvidiaSMISettingCommands(setting) {
				if err := runNVIDIACommand(path, index, arguments...); err != nil {
					return err
				}
			}
			if setting.CoreOffsetMHz != 0 || setting.MemoryOffsetMHz != 0 {
				settingsPath, err := exec.LookPath("nvidia-settings")
				if err != nil {
					return errors.New("nvidia-settings is required for NVIDIA clock offsets")
				}
				arguments := nvidiaSettingsOffsetArguments(index, setting)
				if output, err := exec.Command(settingsPath, arguments...).CombinedOutput(); err != nil {
					return fmt.Errorf("nvidia-settings clock offset failed: %s: %w", strings.TrimSpace(string(output)), err)
				}
			}
			if setting.FanPercent > 0 {
				if err := setNVIDIAFanTargets(settingsPath, index, setting.FanPercent, fansByGPU[index]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func nvidiaSMISettingCommands(setting protocol.GPUOverclock) [][]string {
	var commands [][]string
	if setting.PowerLimitW > 0 {
		commands = append(commands, []string{"-pl", strconv.Itoa(setting.PowerLimitW)})
	}
	if setting.CoreClockMHz > 0 {
		clock := strconv.Itoa(setting.CoreClockMHz)
		commands = append(commands, []string{"-lgc", clock + "," + clock})
	}
	if setting.MemoryClockMHz > 0 {
		clock := strconv.Itoa(setting.MemoryClockMHz)
		commands = append(commands, []string{"-lmc", clock + "," + clock})
	}
	return commands
}

func nvidiaSettingsOffsetArguments(index int, setting protocol.GPUOverclock) []string {
	var arguments []string
	if setting.CoreOffsetMHz != 0 {
		arguments = append(arguments, "-a", fmt.Sprintf("[gpu:%d]/GPUGraphicsClockOffsetAllPerformanceLevels=%d", index, setting.CoreOffsetMHz))
	}
	if setting.MemoryOffsetMHz != 0 {
		arguments = append(arguments, "-a", fmt.Sprintf("[gpu:%d]/GPUMemoryTransferRateOffsetAllPerformanceLevels=%d", index, setting.MemoryOffsetMHz))
	}
	return arguments
}

func preflightNVIDIASettings(smiPath string, settings []protocol.GPUOverclock) error {
	settingsPath := ""
	var fansByGPU map[int][]int
	for _, setting := range settings {
		if setting.CoreOffsetMHz == 0 && setting.MemoryOffsetMHz == 0 && setting.FanPercent == 0 {
			continue
		}
		if settingsPath == "" {
			var err error
			settingsPath, err = exec.LookPath("nvidia-settings")
			if err != nil {
				return errors.New("nvidia-settings is required for NVIDIA fan or clock-offset controls")
			}
		}
		indices, err := selectedNVIDIAIndices(smiPath, setting.Selector)
		if err != nil {
			return err
		}
		for _, index := range indices {
			attribute := "GPUFanControlState"
			if setting.FanPercent == 0 {
				attribute = "GPUGraphicsClockOffsetAllPerformanceLevels"
			}
			output, err := exec.Command(settingsPath, "-q", fmt.Sprintf("[gpu:%d]/%s", index, attribute)).CombinedOutput()
			if err != nil {
				detail := strings.TrimSpace(string(output))
				if detail == "" {
					detail = err.Error()
				}
				return fmt.Errorf("NVIDIA fan or clock-offset controls are unavailable; no tuning changes were applied: %s", detail)
			}
			if setting.FanPercent > 0 {
				if fansByGPU == nil {
					fansByGPU, err = nvidiaFansByGPU(smiPath, settingsPath)
					if err != nil {
						return fmt.Errorf("NVIDIA fan controls are unavailable; no tuning changes were applied: %w", err)
					}
				}
				if len(fansByGPU[index]) == 0 {
					return fmt.Errorf("NVIDIA GPU %d has no mapped fans; no tuning changes were applied", index)
				}
			}
		}
	}
	return nil
}

func selectedNVIDIAIndices(path, selector string) ([]int, error) {
	if selector != "" && selector != "*" {
		index, err := strconv.Atoi(selector)
		if err != nil || index < 0 {
			return nil, fmt.Errorf("invalid NVIDIA GPU selector %q", selector)
		}
		return []int{index}, nil
	}
	output, err := exec.Command(path, "--query-gpu=index", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, fmt.Errorf("list NVIDIA GPUs: %w", err)
	}
	var indices []int
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		index, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil {
			indices = append(indices, index)
		}
	}
	if len(indices) == 0 {
		return nil, errors.New("no NVIDIA GPUs were found")
	}
	return indices, nil
}

func runNVIDIACommand(path string, index int, arguments ...string) error {
	fullArguments := append([]string{"-i", strconv.Itoa(index)}, arguments...)
	if output, err := exec.Command(path, fullArguments...).CombinedOutput(); err != nil {
		return fmt.Errorf("nvidia-smi failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func nvidiaDefaultPower(path string, index int) (int, error) {
	output, err := exec.Command(path, "-i", strconv.Itoa(index), "--query-gpu=power.default_limit", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, err
	}
	return int(value + 0.5), nil
}

func applyAMDOverclock(settings []protocol.GPUOverclock) error {
	for _, setting := range settings {
		if setting.CoreClockMHz != 0 || setting.CoreOffsetMHz != 0 || setting.MemoryClockMHz != 0 || setting.MemoryOffsetMHz != 0 {
			return errors.New("this AMD adapter currently supports power limits and fixed fan speeds only")
		}
		cards, err := selectedAMDCards(setting.Selector)
		if err != nil {
			return err
		}
		for _, card := range cards {
			hwmons, _ := filepath.Glob(filepath.Join(card, "device", "hwmon", "hwmon*"))
			if len(hwmons) == 0 {
				return fmt.Errorf("no hwmon controls found for %s", filepath.Base(card))
			}
			if setting.PowerLimitW > 0 {
				value := strconv.FormatInt(int64(setting.PowerLimitW)*1_000_000, 10)
				if err := os.WriteFile(filepath.Join(hwmons[0], "power1_cap"), []byte(value), 0o600); err != nil {
					return fmt.Errorf("set AMD power limit on %s: %w", filepath.Base(card), err)
				}
			}
			if setting.FanPercent > 0 {
				if err := os.WriteFile(filepath.Join(hwmons[0], "pwm1_enable"), []byte("1"), 0o600); err != nil {
					return fmt.Errorf("enable AMD manual fan on %s: %w", filepath.Base(card), err)
				}
				pwm := strconv.Itoa(setting.FanPercent * 255 / 100)
				if err := os.WriteFile(filepath.Join(hwmons[0], "pwm1"), []byte(pwm), 0o600); err != nil {
					return fmt.Errorf("set AMD fan on %s: %w", filepath.Base(card), err)
				}
			}
		}
	}
	return nil
}

func selectedAMDCards(selector string) ([]string, error) {
	if selector != "" && selector != "*" {
		card := filepath.Join("/sys/class/drm", "card"+selector)
		if !isAMDCard(card) {
			return nil, fmt.Errorf("AMD GPU selector %q was not found", selector)
		}
		return []string{card}, nil
	}
	candidates, _ := filepath.Glob("/sys/class/drm/card[0-9]*")
	result := make([]string, 0, len(candidates))
	for _, card := range candidates {
		if isAMDCard(card) {
			result = append(result, card)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no AMD GPUs were found")
	}
	return result, nil
}

func isAMDCard(card string) bool {
	vendor, err := os.ReadFile(filepath.Join(card, "device", "vendor"))
	return err == nil && strings.TrimSpace(string(vendor)) == "0x1002"
}
