package agent

import (
	"errors"
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

type fakeCPUHost struct {
	files       map[string]string
	accessError map[string]bool
	globs       map[string][]string
	writes      map[string]string
	runs        []string
	runError    error
}

func (f *fakeCPUHost) read(path string) ([]byte, error) {
	value, ok := f.files[path]
	if !ok {
		return nil, errors.New("missing")
	}
	return []byte(value), nil
}
func (f *fakeCPUHost) canAccess(path string) error {
	if f.accessError[path] {
		return errors.New("device is inaccessible")
	}
	if _, ok := f.files[path]; !ok {
		return errors.New("missing")
	}
	return nil
}
func (f *fakeCPUHost) glob(pattern string) ([]string, error) { return f.globs[pattern], nil }
func (f *fakeCPUHost) write(path string, value []byte) error {
	if f.writes == nil {
		f.writes = map[string]string{}
	}
	f.writes[path] = string(value)
	f.files[path] = string(value)
	return nil
}
func (f *fakeCPUHost) run(name string, args ...string) error {
	f.runs = append(f.runs, strings.Join(append([]string{name}, args...), " "))
	return f.runError
}
func TestParseHugePagesAndDetectCPUContext(t *testing.T) {
	host := &fakeCPUHost{
		files: map[string]string{
			"/proc/meminfo":          "MemTotal: 1000 kB\nHugePages_Total: 8\nHugePages_Free: 3\nHugepagesize: 2048 kB\n",
			"/sys/module/msr/refcnt": "1", "/dev/cpu/0/msr": "",
			"/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor": "powersave\n",
		},
		globs: map[string][]string{
			"/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor": {"/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"},
			"/sys/devices/system/node/node[0-9]*":                        {"/sys/devices/system/node/node0", "/sys/devices/system/node/node1"},
		},
	}
	metrics := protocol.Metrics{
		System:          protocol.SystemInfo{CPUCores: 16},
		Miner:           protocol.MinerState{Devices: []protocol.DeviceHashrate{{Kind: "CPU thread"}, {Kind: "CPU thread"}}},
		MiningAlgorithm: "randomx",
	}
	got := detectCPUOptimization(host, "FAILED to apply MSR mod", metrics)
	if got.HugePagesTotal != 8 || got.HugePagesFree != 3 || got.HugePageSizeBytes != 2*1024*1024 || !got.HugePagesCandidate {
		t.Fatalf("huge page detection = %#v", got)
	}
	if !got.MSRModuleLoaded || !got.MSRAccessible || !got.MSRWarning || got.NUMANodes != 2 || got.CPUThreads != 16 || got.MinerThreads != 2 {
		t.Fatalf("CPU diagnostics = %#v", got)
	}
}

func TestMinerWarningsClearWhenCurrentLogsAreClean(t *testing.T) {
	o := newCPUOptimizer(true)
	o.linux = true
	o.host = &fakeCPUHost{files: map[string]string{
		"/proc/meminfo":          "HugePages_Total: 0\n",
		"/sys/module/msr/refcnt": "1",
		"/dev/cpu/0/msr":         "",
	}}
	metrics := cpuMetrics(100)
	status := o.update(metrics, "FAILED TO APPLY MSR MOD, HASHRATE WILL BE LOW")
	if !status.MSRWarning || len(status.MinerWarnings) != 0 {
		t.Fatalf("MSR warning was not summarized without raw log spam: %#v", status)
	}

	status = o.update(metrics, "")
	if status.MSRWarning || len(status.MinerWarnings) != 0 {
		t.Fatalf("stale MSR warning survived clean miner logs: %#v", status)
	}
}

func TestRawMSRWarningsAreNotCopiedToDiagnostics(t *testing.T) {
	host := &fakeCPUHost{files: map[string]string{
		"/proc/meminfo":          "HugePages_Total: 2048\nHugePages_Free: 2048\nHugepagesize: 2048 kB\n",
		"/sys/module/msr/refcnt": "1",
		"/dev/cpu/0/msr":         "",
	}}
	logs := strings.Repeat("\x1b[0m[2026-10-11] msr FAILED TO APPLY MSR MOD, HASHRATE WILL BE LOW", 100) +
		"\x1b[0m NUMA warning failed"
	status := detectCPUOptimization(host, logs, cpuMetrics(100))
	if !status.MSRWarning {
		t.Fatal("structured MSR warning flag was not retained")
	}
	if len(status.MinerWarnings) != 0 {
		t.Fatalf("raw MSR warnings leaked into diagnostics: %#v", status.MinerWarnings)
	}
}

func TestHashrateDecisionThreshold(t *testing.T) {
	if !stableHashrates([]float64{100, 101, 99}) {
		t.Fatal("stable samples rejected")
	}
	if stableHashrates([]float64{100, 110, 90}) {
		t.Fatal("unstable samples accepted")
	}
	if !measurableImprovement(100, 101) || measurableImprovement(100, 100.9) {
		t.Fatal("one-percent acceptance threshold is incorrect")
	}
}

func TestGovernorTrialRollsBackWithoutImprovement(t *testing.T) {
	path := "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"
	host := &fakeCPUHost{files: map[string]string{
		"/proc/meminfo":          "HugePages_Total: 0\nHugePages_Free: 0\nHugepagesize: 2048 kB\n",
		"/sys/module/msr/refcnt": "1", "/dev/cpu/0/msr": "", path: "powersave",
	}, globs: map[string][]string{
		"/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor": {path},
		"/sys/devices/system/node/node[0-9]*":                        {"/sys/devices/system/node/node0"},
	}}
	o := newCPUOptimizer(true)
	o.linux = true
	o.host = host
	m := cpuMetrics(100)
	for i := 0; i < 6; i++ {
		o.update(m, "")
	}
	if host.writes[path] != "powersave" {
		t.Fatalf("governor was not rolled back: %#v", host.writes)
	}
	if o.status.Phase != "rolled_back" {
		t.Fatalf("phase = %q", o.status.Phase)
	}
}

func TestTransitionPausesAndRollsBackActiveTrial(t *testing.T) {
	path := "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"
	host := &fakeCPUHost{files: map[string]string{
		"/proc/meminfo": "HugePages_Total: 0\n", "/sys/module/msr/refcnt": "1", path: "powersave",
	}, globs: map[string][]string{"/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor": {path}}}
	o := newCPUOptimizer(true)
	o.linux = true
	o.host = host
	m := cpuMetrics(100)
	for i := 0; i < 3; i++ {
		o.update(m, "")
	}
	m.MiningState = "returning"
	status := o.update(m, "")
	if status.Phase != "paused" || host.writes[path] != "powersave" || o.trial != "" {
		t.Fatalf("transition was not safe: status=%#v writes=%#v", status, host.writes)
	}
}

func TestMissingMSRModuleSkipsToGovernorTrial(t *testing.T) {
	path := "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"
	host := &fakeCPUHost{
		files: map[string]string{
			"/proc/meminfo": "HugePages_Total: 0\n",
			path:            "powersave",
		},
		globs: map[string][]string{
			"/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor": {path},
		},
	}
	o := newCPUOptimizer(true)
	o.linux = true
	o.host = host
	var status *protocol.CPUOptimizationStatus
	for i := 0; i < cpuTrialSamples; i++ {
		status = o.update(cpuMetrics(100), "")
	}
	if status.Phase != "trial" || o.trial != "governor" || host.writes[path] != "performance" {
		t.Fatalf("governor trial did not follow MSR skip: status=%#v trial=%q writes=%#v", status, o.trial, host.writes)
	}
	if len(status.Skipped) != 1 || status.Skipped[0] != "msr" || len(status.MinerWarnings) == 0 {
		t.Fatalf("MSR skip was not recorded: %#v", status)
	}
}

func TestMSRCharacterDeviceIsCheckedForAccessNotRead(t *testing.T) {
	host := &fakeCPUHost{
		files:       map[string]string{"/proc/meminfo": "HugePages_Total: 1280\nHugePages_Free: 1280\nHugepagesize: 2048 kB\n", "/dev/cpu/0/msr": ""},
		accessError: map[string]bool{"/dev/cpu/0/msr": true},
	}
	status := detectCPUOptimization(host, "", protocol.Metrics{})
	if status.MSRAccessible {
		t.Fatal("inaccessible MSR character device reported as usable")
	}
	host.accessError["/dev/cpu/0/msr"] = false
	status = detectCPUOptimization(host, "", protocol.Metrics{})
	if !status.MSRAccessible {
		t.Fatal("accessible MSR character device was rejected")
	}
	if status.HugePagesCandidate {
		t.Fatal("adequate 2 MiB huge-page pool reported as a candidate")
	}
}

func TestPrepareCPUSelectsSetupModeByAlgorithm(t *testing.T) {
	o := newCPUOptimizer(true)
	o.linux = true
	host := &fakeCPUHost{}
	o.host = host
	for _, algorithm := range []string{"randomx", "qrandomx"} {
		if err := o.prepare(algorithm); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.prepare("yespowermwc"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl start minerdash-cpu-setup@randomx.service",
		"systemctl start minerdash-cpu-setup@randomx.service",
		"systemctl start minerdash-cpu-setup@standard.service",
	}
	if strings.Join(host.runs, "|") != strings.Join(want, "|") {
		t.Fatalf("setup commands = %#v", host.runs)
	}
	host.runError = errors.New("unit not found")
	if err := o.prepare("randomx"); err == nil || !strings.Contains(o.setupWarning, "existing system settings") {
		t.Fatalf("missing setup unit was not surfaced safely: err=%v warning=%q", err, o.setupWarning)
	}
}

func TestRandomXPreparationAndHugePagesAreAlgorithmSpecific(t *testing.T) {
	for _, algorithm := range []string{"randomx", "qrandomx"} {
		if !supportsRandomXOptimization(algorithm) {
			t.Errorf("%s should support RandomX preparation", algorithm)
		}
	}
	if supportsRandomXOptimization("yespowermwc") {
		t.Fatal("non-RandomX algorithm should not reserve RandomX huge pages")
	}
	metrics := cpuMetrics(100)
	metrics.MiningAlgorithm = "yespowermwc"
	status := detectCPUOptimization(&fakeCPUHost{
		files: map[string]string{
			"/proc/meminfo": "HugePages_Total: 128\nHugePages_Free: 0\nHugepagesize: 2048 kB\n",
		},
	}, "", metrics)
	if status.HugePagesCandidate {
		t.Fatal("non-RandomX workload reported a RandomX huge-page candidate")
	}
}

func TestGovernorOptimizationRunsForNonRandomXAlgorithms(t *testing.T) {
	o, host := governorOptimizer()
	metrics := cpuMetrics(100)
	metrics.MiningAlgorithm = "yespowermwc"
	for i := 0; i < cpuTrialSamples; i++ {
		o.update(metrics, "")
	}
	path := "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"
	if o.trial != "governor" || host.writes[path] != "performance" {
		t.Fatalf("non-RandomX governor trial did not start: status=%#v writes=%#v", o.status, host.writes)
	}
	if slices := o.status.Skipped; len(slices) != 0 {
		t.Fatalf("non-RandomX algorithm should not report MSR as skipped: %v", slices)
	}
}

func TestBaselineEarlyOutlierAgesOut(t *testing.T) {
	o, _ := governorOptimizer()
	for _, hashrate := range []float64{40, 100, 101, 99} {
		o.update(cpuMetrics(hashrate), "")
	}
	if o.trial != "governor" || o.status.Phase != "trial" {
		t.Fatalf("stable recent baseline did not advance: phase=%q trial=%q samples=%v", o.status.Phase, o.trial, o.baseline)
	}
}

func TestTrialOutlierAgesOut(t *testing.T) {
	o, _ := governorOptimizer()
	for _, hashrate := range []float64{100, 100, 100} {
		o.update(cpuMetrics(hashrate), "")
	}
	for _, hashrate := range []float64{150, 102, 102, 102} {
		o.update(cpuMetrics(hashrate), "")
	}
	if o.status.Phase != "accepted" {
		t.Fatalf("stable recent trial did not advance: phase=%q samples=%v", o.status.Phase, o.after)
	}
}

func TestOptimizationSampleWindowsAreBounded(t *testing.T) {
	o, _ := governorOptimizer()
	for index := 0; index < 100; index++ {
		hashrate := float64(50 + (index%2)*100)
		o.update(cpuMetrics(hashrate), "")
	}
	if len(o.baseline) > cpuTrialSamples || len(o.after) > cpuTrialSamples {
		t.Fatalf("sample windows grew beyond %d: baseline=%d trial=%d", cpuTrialSamples, len(o.baseline), len(o.after))
	}
}

func TestHeartbeatEvaluationsPreserveSamplesAndAdvanceBaseline(t *testing.T) {
	o, _ := governorOptimizer()
	hashrates := []float64{12399, 12395, 12440}
	for index, hashrate := range hashrates {
		metrics := cpuMetrics(hashrate)
		// Mirror heartbeat evaluation: diagnostics are rebuilt from current metrics each time.
		status := o.update(metrics, "")
		if index < len(hashrates)-1 && status.BaselineSamples != index+1 {
			t.Fatalf("heartbeat %d retained %d samples, want %d", index+1, status.BaselineSamples, index+1)
		}
	}
	if o.status.Phase != "trial" || o.status.BaselineHashrate == 0 || o.trial != "governor" {
		t.Fatalf("baseline did not advance across heartbeat evaluations: %#v", o.status)
	}
	if o.status.BaselineSamples != cpuTrialSamples {
		t.Fatalf("baseline sample count = %d, want %d", o.status.BaselineSamples, cpuTrialSamples)
	}
}

func governorOptimizer() (*cpuOptimizer, *fakeCPUHost) {
	path := "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"
	host := &fakeCPUHost{
		files: map[string]string{
			"/proc/meminfo":          "HugePages_Total: 0\n",
			"/sys/module/msr/refcnt": "1",
			"/dev/cpu/0/msr":         "",
			path:                     "powersave",
		},
		globs: map[string][]string{
			"/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor": {path},
		},
	}
	o := newCPUOptimizer(true)
	o.linux = true
	o.host = host
	return o, host
}

func cpuMetrics(hashrate float64) protocol.Metrics {
	return protocol.Metrics{
		System:     protocol.SystemInfo{CPUCores: 8},
		Miner:      protocol.MinerState{Running: true, Hashrate: hashrate, Devices: []protocol.DeviceHashrate{{Kind: "CPU thread"}}},
		MiningCoin: "QRL", MiningAlgorithm: "qrandomx",
	}
}
