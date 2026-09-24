package agent

import (
	"context"
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

func TestSmartTuneRejectsUnsafeTemperatureBeforeHardwareAccess(t *testing.T) {
	result := runSmartTune(context.Background(), protocol.SmartTuneRequest{
		Mode: "balanced", MaxPowerW: 115, MaxTemperatureC: 0,
	}, nil, nil)
	if result.Error != "unsafe smart tune request" {
		t.Fatalf("error = %q, want unsafe request rejection", result.Error)
	}
}
