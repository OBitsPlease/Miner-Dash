package agent

import (
	"reflect"
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

func TestNVIDIAFansByGPU(t *testing.T) {
	gpuIndices := []int{0, 2, 4}
	tests := []struct {
		name      string
		fanCount  int
		want      map[int][]int
		wantError bool
	}{
		{
			name:     "shared fan controller targets",
			fanCount: 6,
			want:     map[int][]int{0: {0, 1, 2, 3, 4, 5}, 2: {0, 1, 2, 3, 4, 5}, 4: {0, 1, 2, 3, 4, 5}},
		},
		{
			name:     "uneven fan count is supported",
			fanCount: 5,
			want:     map[int][]int{0: {0, 1, 2, 3, 4}, 2: {0, 1, 2, 3, 4}, 4: {0, 1, 2, 3, 4}},
		},
		{name: "no fans", fanCount: 0, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := mapNVIDIAFansToGPUs(gpuIndices, test.fanCount)
			if (err != nil) != test.wantError {
				t.Fatalf("mapNVIDIAFansToGPUs() error = %v, wantError %t", err, test.wantError)
			}
			if err == nil && !reflect.DeepEqual(got, test.want) {
				t.Errorf("mapNVIDIAFansToGPUs() = %v, want %v", got, test.want)
			}
		})
	}
}
