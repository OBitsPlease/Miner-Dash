package agent

import (
	"testing"

	"minerdash/internal/protocol"
)

func TestDesiredFanPercent(t *testing.T) {
	policy := protocol.AutofanPolicy{
		Enabled: true, TargetTemperatureC: 60,
		MinimumFanPercent: 35, MaximumFanPercent: 90,
	}
	tests := []struct {
		temperature float64
		expected    int
	}{
		{50, 35},
		{60, 35},
		{65, 60},
		{90, 90},
	}
	for _, test := range tests {
		if actual := desiredFanPercent(policy, test.temperature); actual != test.expected {
			t.Errorf("desiredFanPercent(%v) = %d, want %d", test.temperature, actual, test.expected)
		}
	}
}
