package agent

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"minerdash/internal/protocol"
)

const cpuTrialSamples = 3

type cpuOptimizationHost interface {
	read(string) ([]byte, error)
	canAccess(string) error
	glob(string) ([]string, error)
	write(string, []byte) error
	run(string, ...string) error
}

type realCPUHost struct{}

func (realCPUHost) read(path string) ([]byte, error) { return os.ReadFile(path) }
func (realCPUHost) canAccess(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return file.Close()
}
func (realCPUHost) glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (realCPUHost) write(path string, value []byte) error { return os.WriteFile(path, value, 0o644) }
func (realCPUHost) run(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

type cpuOptimizer struct {
	mu                sync.Mutex
	enabled           bool
	linux             bool
	host              cpuOptimizationHost
	status            protocol.CPUOptimizationStatus
	key               string
	baseline          []float64
	after             []float64
	trial             string
	originalGovernors map[string]string
	setupWarning      string
}

func newCPUOptimizer(enabled bool) *cpuOptimizer {
	return &cpuOptimizer{enabled: enabled, linux: runtime.GOOS == "linux", host: realCPUHost{}, originalGovernors: map[string]string{}}
}

func (o *cpuOptimizer) prepare(algorithm string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.enabled || !o.linux {
		return nil
	}
	mode := "standard"
	if supportsRandomXOptimization(algorithm) {
		mode = "randomx"
	}
	err := o.host.run("systemctl", "start", "minerdash-cpu-setup@"+mode+".service")
	if err != nil {
		o.setupWarning = "CPU setup service did not run; mining will continue with existing system settings: " + err.Error()
		return err
	}
	o.setupWarning = ""
	return nil
}

func supportsRandomXOptimization(algorithm string) bool {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "randomx", "qrandomx":
		return true
	default:
		return false
	}
}

func (o *cpuOptimizer) update(metrics protocol.Metrics, logs string) *protocol.CPUOptimizationStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := detectCPUOptimization(o.host, logs, metrics)
	s.Enabled = o.enabled && o.linux
	s.Coin, s.Algorithm = metrics.MiningCoin, metrics.MiningAlgorithm
	if !s.Enabled {
		s.Phase, s.Message = "disabled", "CPU optimization diagnostics are disabled or unsupported on this OS"
		o.status = s
		return cloneCPUStatus(s)
	}
	s.Phase, s.Message = o.status.Phase, o.status.Message
	s.Applied, s.LastError = append([]string(nil), o.status.Applied...), o.status.LastError
	s.Skipped = append([]string(nil), o.status.Skipped...)
	if o.setupWarning != "" {
		s.MinerWarnings = appendUnique(s.MinerWarnings, o.setupWarning)
	}
	s.BaselineHashrate, s.TrialHashrate, s.ImprovementPercent = o.status.BaselineHashrate, o.status.TrialHashrate, o.status.ImprovementPercent
	cpuOnly := metrics.Miner.Running && len(metrics.GPUs) == 0 && (s.MinerThreads > 0 || metrics.Miner.Hashrate > 0)
	transition := metrics.MiningState == "waiting" || metrics.MiningState == "returning"
	key := workloadKey(metrics.MiningCoin, metrics.MiningAlgorithm)
	if !cpuOnly {
		if o.trial != "" {
			o.rollback(&s)
		}
		s.Phase, s.Message = "diagnostics", "Waiting for a Linux CPU-only mining workload"
		o.resetSamples(key)
	} else if transition || key == "|" || metrics.Miner.Hashrate <= 0 {
		if o.trial != "" {
			o.rollback(&s)
		}
		s.Phase, s.Message = "paused", "Optimization trials paused during workload transition or unstable telemetry"
		o.resetSamples(key)
	} else {
		o.advance(&s, key, metrics.Miner.Hashrate)
	}
	s.BaselineSamples, s.TrialSamples = len(o.baseline), len(o.after)
	o.status = s
	return cloneCPUStatus(s)
}

func workloadKey(coin, algorithm string) string {
	return strings.ToLower(strings.TrimSpace(coin)) + "|" + strings.ToLower(strings.TrimSpace(algorithm))
}

func (o *cpuOptimizer) resetSamples(key string) {
	o.key, o.baseline, o.after = key, nil, nil
}

func (o *cpuOptimizer) advance(s *protocol.CPUOptimizationStatus, key string, hashrate float64) {
	if key != o.key {
		o.rollback(s)
		o.resetSamples(key)
	}
	if o.trial == "" {
		o.baseline = appendRecent(o.baseline, hashrate)
		s.Phase, s.Message = "baseline", "Collecting stable per-coin/per-algorithm baseline"
		if len(o.baseline) < cpuTrialSamples || !stableHashrates(o.baseline) {
			return
		}
		s.BaselineHashrate = average(o.baseline)
		switch {
		case supportsRandomXOptimization(s.Algorithm) && !s.MSRModuleLoaded:
			s.Skipped = appendUnique(s.Skipped, "msr")
			s.MinerWarnings = appendUnique(s.MinerWarnings, "MSR module is not loaded; the boot-time CPU setup service may be unavailable")
			if !hasNonPerformanceGovernor(s.Governors) {
				s.Phase, s.Message = "complete", "MSR module unavailable and no governor optimization candidate is available"
				return
			}
			if err := o.startGovernorTrial(s); err != nil {
				s.Phase, s.LastError, s.Message = "error", err.Error(), "Optimization trial could not be applied"
				o.rollback(s)
				return
			}
		case hasNonPerformanceGovernor(s.Governors):
			if err := o.startGovernorTrial(s); err != nil {
				s.Phase, s.LastError, s.Message = "error", err.Error(), "Optimization trial could not be applied"
				o.rollback(s)
				return
			}
		default:
			s.Phase, s.Message = "complete", "CPU diagnostics complete; current governor is already performance"
			return
		}
		o.after = nil
		s.Phase, s.Message = "trial", "Measuring "+o.trial+" optimization"
		return
	}
	o.after = appendRecent(o.after, hashrate)
	s.Phase = "trial"
	if len(o.after) < cpuTrialSamples || !stableHashrates(o.after) {
		return
	}
	s.TrialHashrate = average(o.after)
	s.ImprovementPercent = 100 * (s.TrialHashrate/s.BaselineHashrate - 1)
	if measurableImprovement(s.BaselineHashrate, s.TrialHashrate) {
		s.Applied = appendUnique(s.Applied, o.trial)
		s.Phase, s.Message = "accepted", fmt.Sprintf("Accepted %s after %.2f%% measured improvement", o.trial, s.ImprovementPercent)
		o.originalGovernors = map[string]string{}
	} else {
		s.Phase, s.Message = "rolled_back", "Rolled back optimization without measurable improvement"
		o.rollback(s)
	}
	o.trial, o.baseline, o.after = "", []float64{s.TrialHashrate}, nil
}

func (o *cpuOptimizer) startGovernorTrial(s *protocol.CPUOptimizationStatus) error {
	o.originalGovernors = copyStrings(s.Governors)
	for path := range s.Governors {
		if err := o.host.write(path, []byte("performance")); err != nil {
			return err
		}
	}
	o.trial = "governor"
	return nil
}

func (o *cpuOptimizer) rollback(s *protocol.CPUOptimizationStatus) {
	var failures []string
	for path, governor := range o.originalGovernors {
		if err := o.host.write(path, []byte(governor)); err != nil {
			failures = append(failures, err.Error())
		}
	}
	o.originalGovernors, o.trial = map[string]string{}, ""
	if len(failures) > 0 {
		s.LastError = strings.Join(failures, "; ")
	}
}

func detectCPUOptimization(host cpuOptimizationHost, logs string, metrics protocol.Metrics) protocol.CPUOptimizationStatus {
	s := protocol.CPUOptimizationStatus{Phase: "diagnostics", CPUThreads: metrics.System.CPUCores}
	if data, err := host.read("/proc/meminfo"); err == nil {
		s.HugePagesTotal, s.HugePagesFree, s.HugePageSizeBytes = parseHugePages(string(data))
	}
	s.HugePagesCandidate = supportsRandomXOptimization(metrics.MiningAlgorithm) &&
		(s.HugePageSizeBytes != 2*1024*1024 || s.HugePagesFree < 1280)
	if _, err := host.read("/sys/module/msr/refcnt"); err == nil {
		s.MSRModuleLoaded = true
	}
	if err := host.canAccess("/dev/cpu/0/msr"); err == nil {
		s.MSRAccessible = true
	}
	lowerLogs := strings.ToLower(logs)
	s.MSRWarning = strings.Contains(lowerLogs, "msr") && (strings.Contains(lowerLogs, "failed") || strings.Contains(lowerLogs, "cannot") || strings.Contains(lowerLogs, "permission"))
	for _, line := range strings.FieldsFunc(logs, func(character rune) bool {
		return character == '\n' || character == '\r'
	}) {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "msr") &&
			(strings.Contains(lower, "huge page") || strings.Contains(lower, "numa")) &&
			(strings.Contains(lower, "warn") || strings.Contains(lower, "fail") || strings.Contains(lower, "cannot") || strings.Contains(lower, "permission")) {
			s.MinerWarnings = appendUnique(s.MinerWarnings, strings.TrimSpace(line))
			if len(s.MinerWarnings) == 3 {
				break
			}
		}
	}
	governors, _ := host.glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor")
	s.Governors = map[string]string{}
	for _, path := range governors {
		if value, err := host.read(path); err == nil {
			s.Governors[path] = strings.TrimSpace(string(value))
		}
	}
	nodes, _ := host.glob("/sys/devices/system/node/node[0-9]*")
	s.NUMANodes = len(nodes)
	for _, device := range metrics.Miner.Devices {
		if strings.EqualFold(device.Kind, "CPU thread") {
			s.MinerThreads++
		}
	}
	return s
}

func parseHugePages(input string) (total, free, size uint64) {
	for _, line := range strings.Split(input, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "HugePages_Total":
			total = value
		case "HugePages_Free":
			free = value
		case "Hugepagesize":
			size = value * 1024
		}
	}
	return
}

func stableHashrates(values []float64) bool {
	if len(values) < cpuTrialSamples {
		return false
	}
	mean := average(values)
	if mean <= 0 {
		return false
	}
	for _, value := range values {
		if math.Abs(value-mean)/mean > .03 {
			return false
		}
	}
	return true
}
func appendRecent(values []float64, value float64) []float64 {
	values = append(values, value)
	if len(values) > cpuTrialSamples {
		values = values[len(values)-cpuTrialSamples:]
	}
	return values
}
func measurableImprovement(before, after float64) bool { return before > 0 && after >= before*1.01 }
func average(v []float64) float64 {
	var n float64
	for _, x := range v {
		n += x
	}
	return n / float64(len(v))
}
func hasNonPerformanceGovernor(m map[string]string) bool {
	for _, v := range m {
		if v != "performance" {
			return true
		}
	}
	return false
}
func copyStrings(m map[string]string) map[string]string {
	n := map[string]string{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
func cloneCPUStatus(s protocol.CPUOptimizationStatus) *protocol.CPUOptimizationStatus {
	s.Governors = copyStrings(s.Governors)
	s.Applied = append([]string(nil), s.Applied...)
	s.Skipped = append([]string(nil), s.Skipped...)
	s.MinerWarnings = append([]string(nil), s.MinerWarnings...)
	sort.Strings(s.Applied)
	sort.Strings(s.Skipped)
	return &s
}
