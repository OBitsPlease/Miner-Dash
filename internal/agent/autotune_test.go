package agent

import (
	"context"
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

func TestBalancedTuneScoreRejectsExcessiveHashrateLoss(t *testing.T) {
	baseline := tuneMeasurement{hashrate: 40, power: 115}
	if score := balancedTuneScore(tuneMeasurement{hashrate: 38, power: 100}, baseline); score != 0 {
		t.Fatalf("score = %v, want rejection", score)
	}
}

func TestBalancedTuneScoreRewardsEfficiencyWithoutLargeHashrateLoss(t *testing.T) {
	baseline := tuneMeasurement{hashrate: 40, power: 115}
	candidate := tuneMeasurement{hashrate: 39.5, power: 105}
	if balancedTuneScore(candidate, baseline) <= balancedTuneScore(baseline, baseline) {
		t.Fatal("efficient candidate did not beat baseline")
	}
}

func TestSmartTunePowerCeilingUsesConfiguredOrHardwareMaximum(t *testing.T) {
	gpu := powerRange{current: 115, minimum: 80, maximum: 250}
	if got := smartTunePowerCeiling(gpu, 0); got != 250 {
		t.Fatalf("hardware ceiling = %d, want 250", got)
	}
	if got := smartTunePowerCeiling(gpu, 115); got != 115 {
		t.Fatalf("configured ceiling = %d, want 115", got)
	}
	if got := smartTunePowerCeiling(gpu, 300); got != 250 {
		t.Fatalf("clamped ceiling = %d, want 250", got)
	}
}

func TestSmartTuneRejectsUnsafeTemperatureBeforeHardwareAccess(t *testing.T) {
	result := runSmartTune(context.Background(), protocol.SmartTuneRequest{
		Mode: "balanced", MaxPowerW: 115, MaxTemperatureC: 0,
	}, 115, nil, nil)
	if result.Error != "unsafe smart tune request" {
		t.Fatalf("error = %q, want unsafe request rejection", result.Error)
	}
}

func TestMinerGPUHashrateMatchesOnlyGPUDeviceAtIndex(t *testing.T) {
	state := protocol.MinerState{Devices: []protocol.DeviceHashrate{
		{Index: 0, Kind: "CPU", Hashrate: 900},
		{Index: 1, Kind: "gpu", Hashrate: 42.5},
	}}
	if got := minerGPUHashrate(state, 1); got != 42.5 {
		t.Fatalf("GPU 1 hashrate = %v, want 42.5", got)
	}
	if got := minerGPUHashrate(state, 0); got != 0 {
		t.Fatalf("CPU device leaked into GPU lookup: %v", got)
	}
	if got := minerGPUHashrate(state, 7); got != 0 {
		t.Fatalf("missing GPU hashrate = %v, want 0", got)
	}
}

func TestSmartTunePreflightRequiresRunningMinerBeforeChangingPowerLimits(t *testing.T) {
	err := smartTunePreflight(NewMinerManager(nil), []powerRange{{index: 0, current: 115, minimum: 80, maximum: 250}})
	if err == nil {
		t.Fatal("preflight accepted a stopped miner")
	}
	if !strings.Contains(err.Error(), "must be running") {
		t.Fatalf("error = %q, want a running-miner explanation", err.Error())
	}
}

func TestWatchdogDoesNotRestartTheMinerWhileSmartTuneIsMeasuring(t *testing.T) {
	client := &Client{miner: NewMinerManager(nil), tuner: &smartTuner{}}
	client.configuration.Watchdog = protocol.WatchdogPolicy{
		Enabled: true, MaxTemperatureC: 80, MinHashrate: 1000, RestartAfterSeconds: 60,
	}
	client.tuner.status = protocol.SmartTuneStatus{Active: true, CommandID: "tune-1"}
	metrics := protocol.Metrics{Miner: protocol.MinerState{Running: true, Hashrate: 0}}
	if err := client.enforceWatchdog(metrics); err != nil {
		t.Fatalf("watchdog returned %v", err)
	}
	if !client.lowHashSince.IsZero() {
		t.Fatal("watchdog started a low-hashrate restart timer during smart tune")
	}
}
