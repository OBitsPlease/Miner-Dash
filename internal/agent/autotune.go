package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"minerdash/internal/protocol"
)

const (
	smartTunePowerStep   = 5
	smartTunePowerRange  = 10
	smartTuneSettleTime  = 20 * time.Second
	smartTuneSampleCount = 3
	smartTuneSampleDelay = 5 * time.Second
)

type smartTuner struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	status protocol.SmartTuneStatus
}

type powerRange struct {
	index   int
	current int
	minimum int
}

type tuneMeasurement struct {
	hashrate    float64
	power       float64
	temperature float64
}

func (t *smartTuner) snapshot() *protocol.SmartTuneStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status.CommandID == "" {
		return nil
	}
	status := t.status
	status.SelectedSettings = append([]protocol.GPUOverclock(nil), t.status.SelectedSettings...)
	return &status
}

func (t *smartTuner) active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status.Active
}

func (t *smartTuner) start(parent context.Context, command protocol.Command, miner *MinerManager, logger interface{ Printf(string, ...any) }, finish func(protocol.CommandResult)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status.Active {
		return errors.New("smart tune is already running")
	}
	if command.SmartTune == nil {
		return errors.New("smart tune settings are missing")
	}
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	t.status = protocol.SmartTuneStatus{
		Active: true, CommandID: command.ID, Phase: "starting",
		Message: "Reading safe NVIDIA power ranges", StartedAt: time.Now().UTC(),
	}
	request := *command.SmartTune
	go func() {
		result := runSmartTune(ctx, request, miner, t.update)
		t.mu.Lock()
		t.cancel = nil
		t.status.Active = false
		if result.Error != "" {
			t.status.Phase = "failed"
			t.status.Message = result.Error
		} else {
			t.status.Phase = "complete"
			t.status.Message = result.Output
			t.status.SelectedSettings = append([]protocol.GPUOverclock(nil), result.TunedSettings...)
		}
		t.mu.Unlock()
		finish(result)
		if result.Error != "" {
			logger.Printf("smart tune failed: %s", result.Error)
		}
	}()
	return nil
}

func (t *smartTuner) stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.status.Active || t.cancel == nil {
		return false
	}
	t.status.Phase = "stopping"
	t.status.Message = "Stopping and restoring original power limits"
	t.cancel()
	return true
}

func (t *smartTuner) update(status protocol.SmartTuneStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status.Active = true
	status.CommandID = t.status.CommandID
	status.StartedAt = t.status.StartedAt
	t.status = status
}

func runSmartTune(ctx context.Context, request protocol.SmartTuneRequest, miner *MinerManager, update func(protocol.SmartTuneStatus)) protocol.CommandResult {
	if request.Mode != "balanced" || request.MaxPowerW <= 0 || request.MaxPowerW > 115 ||
		request.MaxTemperatureC < 60 || request.MaxTemperatureC > 85 {
		return protocol.CommandResult{Error: "unsafe smart tune request"}
	}
	ranges, err := readNVIDIAPowerRanges()
	if err != nil {
		return protocol.CommandResult{Error: err.Error()}
	}
	if len(ranges) == 0 {
		return protocol.CommandResult{Error: "no NVIDIA GPUs were found"}
	}
	original := make([]protocol.GPUOverclock, 0, len(ranges))
	selected := make([]protocol.GPUOverclock, 0, len(ranges))
	for _, gpu := range ranges {
		limit := min(gpu.current, request.MaxPowerW)
		original = append(original, protocol.GPUOverclock{Selector: strconv.Itoa(gpu.index), PowerLimitW: limit})
		if err := setNVIDIAPowerLimit(gpu.index, limit); err != nil {
			restorePowerLimits(original)
			return protocol.CommandResult{Error: err.Error()}
		}
	}
	restoreOnFailure := true
	defer func() {
		if restoreOnFailure {
			restorePowerLimits(original)
		}
	}()
	for position, gpu := range ranges {
		if err := ctx.Err(); err != nil {
			return protocol.CommandResult{Error: "smart tune canceled; original power limits restored"}
		}
		startLimit := min(gpu.current, request.MaxPowerW)
		update(protocol.SmartTuneStatus{
			Phase: "benchmarking", GPUIndex: gpu.index, CompletedGPUs: position, TotalGPUs: len(ranges),
			Message: fmt.Sprintf("Benchmarking GPU %d at %d W", gpu.index, startLimit), SelectedSettings: selected,
		})
		baseline, err := measureGPU(ctx, miner, gpu.index, request.MaxPowerW, request.MaxTemperatureC, 0)
		if err != nil {
			return protocol.CommandResult{Error: fmt.Sprintf("GPU %d baseline failed: %v", gpu.index, err)}
		}
		bestLimit, bestMeasurement := startLimit, baseline
		for candidate := startLimit - smartTunePowerStep; candidate >= max(gpu.minimum, startLimit-smartTunePowerRange); candidate -= smartTunePowerStep {
			if err := setNVIDIAPowerLimit(gpu.index, candidate); err != nil {
				return protocol.CommandResult{Error: fmt.Sprintf("GPU %d: %v", gpu.index, err)}
			}
			update(protocol.SmartTuneStatus{
				Phase: "testing", GPUIndex: gpu.index, CompletedGPUs: position, TotalGPUs: len(ranges),
				Message: fmt.Sprintf("Testing GPU %d at %d W", gpu.index, candidate), SelectedSettings: selected,
			})
			measurement, err := measureGPU(ctx, miner, gpu.index, request.MaxPowerW, request.MaxTemperatureC, smartTuneSettleTime)
			if err != nil {
				return protocol.CommandResult{Error: fmt.Sprintf("GPU %d test failed: %v", gpu.index, err)}
			}
			if balancedTuneScore(measurement, baseline) > balancedTuneScore(bestMeasurement, baseline) {
				bestLimit, bestMeasurement = candidate, measurement
			}
		}
		if err := setNVIDIAPowerLimit(gpu.index, bestLimit); err != nil {
			return protocol.CommandResult{Error: fmt.Sprintf("GPU %d final limit failed: %v", gpu.index, err)}
		}
		selected = append(selected, protocol.GPUOverclock{Selector: strconv.Itoa(gpu.index), PowerLimitW: bestLimit})
	}
	restoreOnFailure = false
	sort.Slice(selected, func(i, j int) bool { return selected[i].Selector < selected[j].Selector })
	return protocol.CommandResult{
		Output:        fmt.Sprintf("Balanced smart tune completed for %d GPUs; all limits are at or below %d W", len(selected), request.MaxPowerW),
		TunedSettings: selected,
	}
}

func balancedTuneScore(measurement, baseline tuneMeasurement) float64 {
	if baseline.hashrate <= 0 || baseline.power <= 0 || measurement.hashrate < baseline.hashrate*0.97 || measurement.power <= 0 {
		return 0
	}
	hashRatio := measurement.hashrate / baseline.hashrate
	efficiencyRatio := (measurement.hashrate / measurement.power) / (baseline.hashrate / baseline.power)
	return hashRatio*0.6 + efficiencyRatio*0.4
}

func measureGPU(ctx context.Context, miner *MinerManager, index int, maxPower int, maxTemperature float64, settle time.Duration) (tuneMeasurement, error) {
	if err := waitForTune(ctx, settle); err != nil {
		return tuneMeasurement{}, err
	}
	var result tuneMeasurement
	for sample := 0; sample < smartTuneSampleCount; sample++ {
		if err := waitForTune(ctx, smartTuneSampleDelay); err != nil {
			return tuneMeasurement{}, err
		}
		state := miner.State()
		if !state.Running {
			return tuneMeasurement{}, errors.New("miner stopped")
		}
		hashrate := 0.0
		for _, device := range state.Devices {
			if device.Index == index && strings.EqualFold(device.Kind, "GPU") {
				hashrate = device.Hashrate
				break
			}
		}
		gpus := nvidiaGPUs()
		var found bool
		for _, gpu := range gpus {
			if gpu.Index != index {
				continue
			}
			found = true
			if gpu.Power > float64(maxPower)+1 {
				return tuneMeasurement{}, fmt.Errorf("power draw %.1f W exceeded the %d W safety limit", gpu.Power, maxPower)
			}
			if maxTemperature > 0 && gpu.Temperature >= maxTemperature {
				return tuneMeasurement{}, fmt.Errorf("temperature %.1f C reached the %.1f C safety limit", gpu.Temperature, maxTemperature)
			}
			result.hashrate += hashrate
			result.power += gpu.Power
			result.temperature += gpu.Temperature
			break
		}
		if !found || hashrate <= 0 {
			return tuneMeasurement{}, errors.New("GPU telemetry is unavailable")
		}
	}
	result.hashrate /= smartTuneSampleCount
	result.power /= smartTuneSampleCount
	result.temperature /= smartTuneSampleCount
	return result, nil
}

func readNVIDIAPowerRanges() ([]powerRange, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, errors.New("nvidia-smi is required for smart tune")
	}
	output, err := exec.Command(path, "--query-gpu=index,power.limit,power.min_limit", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, fmt.Errorf("read NVIDIA power limits: %w", err)
	}
	var result []powerRange
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 3 {
			return nil, errors.New("unexpected NVIDIA power limit output")
		}
		index, indexErr := strconv.Atoi(strings.TrimSpace(fields[0]))
		current, currentErr := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		minimum, minimumErr := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
		if indexErr != nil || currentErr != nil || minimumErr != nil {
			return nil, errors.New("invalid NVIDIA power limit output")
		}
		result = append(result, powerRange{index: index, current: int(current + 0.5), minimum: int(minimum + 0.5)})
	}
	return result, nil
}

func setNVIDIAPowerLimit(index, watts int) error {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return err
	}
	return runNVIDIACommand(path, index, "-pl", strconv.Itoa(watts))
}

func restorePowerLimits(settings []protocol.GPUOverclock) {
	for _, setting := range settings {
		index, err := strconv.Atoi(setting.Selector)
		if err == nil {
			_ = setNVIDIAPowerLimit(index, setting.PowerLimitW)
		}
	}
}

func waitForTune(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
