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
	for _, setting := range settings {
		indices, err := selectedNVIDIAIndices(path, setting.Selector)
		if err != nil {
			return err
		}
		for _, index := range indices {
			if setting.PowerLimitW > 0 {
				if err := runNVIDIACommand(path, index, "-pl", strconv.Itoa(setting.PowerLimitW)); err != nil {
					return err
				}
			}
			if setting.CoreClockMHz > 0 {
				clock := strconv.Itoa(setting.CoreClockMHz)
				if err := runNVIDIACommand(path, index, "-lgc", clock+","+clock); err != nil {
					return err
				}
			}
			if setting.MemoryClockMHz > 0 {
				clock := strconv.Itoa(setting.MemoryClockMHz)
				if err := runNVIDIACommand(path, index, "-lmc", clock+","+clock); err != nil {
					return err
				}
			}
			if setting.CoreOffsetMHz != 0 || setting.MemoryOffsetMHz != 0 {
				settingsPath, err := exec.LookPath("nvidia-settings")
				if err != nil {
					return errors.New("nvidia-settings is required for NVIDIA clock offsets")
				}
				arguments := []string{}
				if setting.CoreOffsetMHz != 0 {
					arguments = append(arguments, "-a", fmt.Sprintf("[gpu:%d]/GPUGraphicsClockOffsetAllPerformanceLevels=%d", index, setting.CoreOffsetMHz))
				}
				if setting.MemoryOffsetMHz != 0 {
					arguments = append(arguments, "-a", fmt.Sprintf("[gpu:%d]/GPUMemoryTransferRateOffsetAllPerformanceLevels=%d", index, setting.MemoryOffsetMHz))
				}
				if output, err := exec.Command(settingsPath, arguments...).CombinedOutput(); err != nil {
					return fmt.Errorf("nvidia-settings clock offset failed: %s: %w", strings.TrimSpace(string(output)), err)
				}
			}
			if setting.FanPercent > 0 {
				if err := setNVIDIAFan(index, setting.FanPercent); err != nil {
					return err
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
