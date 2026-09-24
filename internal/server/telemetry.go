package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

const historyRetention = 32 * 24 * time.Hour
const driverEfficiencyMinimumSamples = 60

func (s *Store) recordMetricsLocked(rigID string, metrics protocol.Metrics) error {
	if last := s.state.HistoryRecordedAt[rigID]; !last.IsZero() && metrics.CollectedAt.Sub(last) < time.Minute {
		return nil
	}
	if err := s.appendMetric(rigID, metrics); err != nil {
		return fmt.Errorf("store telemetry: %w", err)
	}
	s.recordDriverEfficiencyLocked(rigID, metrics)
	s.state.HistoryRecordedAt[rigID] = metrics.CollectedAt
	return s.pruneHistory(metrics.CollectedAt)
}

func (s *Store) gpuWorkloadLocked(flightSheetID string) (string, string) {
	sheet, ok := s.state.FlightSheets[flightSheetID]
	if !ok {
		return "", ""
	}
	if strings.EqualFold(sheet.DeviceType, "gpu") {
		return strings.ToUpper(sheet.Coin), sheet.Algorithm
	}
	if strings.EqualFold(sheet.SecondaryDeviceType, "gpu") {
		return strings.ToUpper(sheet.SecondaryCoin), sheet.SecondaryAlgorithm
	}
	return "", ""
}

func (s *Store) recordDriverEfficiencyLocked(rigID string, metrics protocol.Metrics) {
	if !metrics.Miner.Running || metrics.MiningCoin == "" || metrics.MiningAlgorithm == "" || len(metrics.GPUs) == 0 {
		return
	}
	rig := s.state.Rigs[rigID]
	overheadPerGPU := rig.Desired.PowerOffsetW / float64(len(metrics.GPUs))
	for _, gpu := range metrics.GPUs {
		if gpu.Driver == "" {
			continue
		}
		hashrate, unit := gpuHashrate(metrics.Miner, gpu.Index, len(metrics.GPUs))
		power := gpu.Power + overheadPerGPU
		if hashrate <= 0 || power <= 0 || unit == "" {
			continue
		}
		record := protocol.DriverEfficiencyRecord{
			Vendor: strings.ToUpper(gpu.Vendor), Model: gpu.Name, Driver: gpu.Driver,
			Coin: metrics.MiningCoin, Algorithm: metrics.MiningAlgorithm, HashrateUnit: unit,
			Miner: metrics.Miner.Profile, Tuning: s.gpuTuningLocked(rig, gpu),
		}
		key := driverEfficiencyKey(record)
		existing := s.state.DriverEfficiency[key]
		if existing == nil {
			record.FirstSampleAt = metrics.CollectedAt
			existing = &record
			s.state.DriverEfficiency[key] = existing
		}
		existing.Samples++
		existing.TotalHashrate += hashrate
		existing.TotalPowerW += power
		existing.LastSampleAt = metrics.CollectedAt
	}
}

func gpuHashrate(miner protocol.MinerState, gpuIndex, gpuCount int) (float64, string) {
	for _, device := range miner.Devices {
		if device.Index == gpuIndex && !strings.EqualFold(device.Kind, "CPU thread") {
			return device.Hashrate, device.Unit
		}
	}
	if gpuCount == 1 {
		return miner.Hashrate, miner.HashrateUnit
	}
	return 0, ""
}

func driverEfficiencyKey(record protocol.DriverEfficiencyRecord) string {
	return strings.Join([]string{
		strings.ToUpper(record.Vendor), record.Model, record.Driver, strings.ToUpper(record.Coin),
		strings.ToLower(record.Algorithm), record.HashrateUnit, record.Miner,
		fmt.Sprintf("%d:%d:%d:%d:%d:%d", record.Tuning.CoreClockMHz, record.Tuning.CoreOffsetMHz,
			record.Tuning.MemoryClockMHz, record.Tuning.MemoryOffsetMHz, record.Tuning.PowerLimitW, record.Tuning.FanPercent),
	}, "\x1f")
}

func (s *Store) gpuTuningLocked(rig *storedRig, gpu protocol.GPU) protocol.GPUOverclock {
	profile, ok := s.state.OverclockProfiles[rig.Desired.OverclockProfileID]
	if !ok || !strings.EqualFold(profile.Vendor, gpu.Vendor) {
		return protocol.GPUOverclock{Selector: fmt.Sprint(gpu.Index)}
	}
	var wildcard protocol.GPUOverclock
	for _, setting := range profile.Settings {
		if setting.Selector == fmt.Sprint(gpu.Index) {
			setting.Selector = fmt.Sprint(gpu.Index)
			return setting
		}
		if setting.Selector == "*" {
			wildcard = setting
		}
	}
	wildcard.Selector = fmt.Sprint(gpu.Index)
	return wildcard
}

func driverEfficiency(record *protocol.DriverEfficiencyRecord) float64 {
	if record == nil || record.TotalPowerW <= 0 {
		return 0
	}
	return record.TotalHashrate / record.TotalPowerW
}

func (s *Store) driverEfficiencySummariesLocked(rig *protocol.Rig) []protocol.DriverEfficiencySummary {
	coin, algorithm := s.gpuWorkloadLocked(rig.Desired.FlightSheetID)
	if coin == "" || algorithm == "" {
		return nil
	}
	summaries := make([]protocol.DriverEfficiencySummary, 0, len(rig.Metrics.GPUs))
	for _, gpu := range rig.Metrics.GPUs {
		currentTuning := s.gpuTuningLocked(s.state.Rigs[rig.ID], gpu)
		summary := protocol.DriverEfficiencySummary{
			GPUIndex: gpu.Index, Vendor: strings.ToUpper(gpu.Vendor), Model: gpu.Name,
			Coin: coin, Algorithm: algorithm, CurrentDriver: gpu.Driver, Status: "collecting",
		}
		var current *protocol.DriverEfficiencyRecord
		var best *protocol.DriverEfficiencyRecord
		testedDrivers := make(map[string]struct{})
		for _, record := range s.state.DriverEfficiency {
			if !strings.EqualFold(record.Vendor, gpu.Vendor) || record.Model != gpu.Name ||
				!strings.EqualFold(record.Coin, coin) || !strings.EqualFold(record.Algorithm, algorithm) ||
				record.Miner != rig.Metrics.Miner.Profile || !sameTuning(record.Tuning, currentTuning) {
				continue
			}
			if strings.EqualFold(record.Driver, gpu.Driver) {
				current = record
				summary.HashrateUnit = record.HashrateUnit
			}
			if record.Samples >= driverEfficiencyMinimumSamples {
				testedDrivers[record.Driver] = struct{}{}
				if best == nil || driverEfficiency(record) > driverEfficiency(best) {
					best = record
				}
			}
		}
		summary.ComparedDrivers = len(testedDrivers)
		if current != nil {
			summary.CurrentSamples = current.Samples
			summary.CurrentEfficiency = driverEfficiency(current)
		}
		if best != nil {
			summary.BestDriver = best.Driver
			summary.BestEfficiency = driverEfficiency(best)
			summary.BestSamples = best.Samples
			summary.HashrateUnit = best.HashrateUnit
		}
		if current != nil && current.Samples >= driverEfficiencyMinimumSamples && best != nil {
			if strings.EqualFold(current.Driver, best.Driver) {
				summary.Status = "current-best"
			} else if summary.CurrentEfficiency > 0 && summary.BestEfficiency > summary.CurrentEfficiency*1.02 {
				summary.Status = "better-tested"
				summary.ImprovementPercent = 100 * (summary.BestEfficiency/summary.CurrentEfficiency - 1)
			} else {
				summary.Status = "similar"
			}
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].GPUIndex < summaries[j].GPUIndex })
	return summaries
}

func (s *Store) overclockKnowledgePresetsLocked(rig *protocol.Rig) []protocol.OverclockKnowledgePreset {
	coin, algorithm := s.gpuWorkloadLocked(rig.Desired.FlightSheetID)
	if coin == "" || algorithm == "" {
		return nil
	}
	presets := make([]protocol.OverclockKnowledgePreset, 0)
	for _, gpu := range rig.Metrics.GPUs {
		for _, record := range s.state.DriverEfficiency {
			if record.Samples < driverEfficiencyMinimumSamples || tuningIsEmpty(record.Tuning) ||
				!strings.EqualFold(record.Vendor, gpu.Vendor) || record.Model != gpu.Name ||
				!strings.EqualFold(record.Coin, coin) || !strings.EqualFold(record.Algorithm, algorithm) {
				continue
			}
			tuning := record.Tuning
			tuning.Selector = fmt.Sprint(gpu.Index)
			presets = append(presets, protocol.OverclockKnowledgePreset{
				GPUIndex: gpu.Index, Vendor: record.Vendor, Model: record.Model, Driver: record.Driver,
				Coin: record.Coin, Algorithm: record.Algorithm, Miner: record.Miner,
				HashrateUnit: record.HashrateUnit, Efficiency: driverEfficiency(record),
				Samples: record.Samples, Tuning: tuning,
			})
		}
	}
	sort.Slice(presets, func(i, j int) bool {
		if presets[i].GPUIndex != presets[j].GPUIndex {
			return presets[i].GPUIndex < presets[j].GPUIndex
		}
		return presets[i].Efficiency > presets[j].Efficiency
	})
	return presets
}

func sameTuning(left, right protocol.GPUOverclock) bool {
	return left.CoreClockMHz == right.CoreClockMHz &&
		left.CoreOffsetMHz == right.CoreOffsetMHz &&
		left.MemoryClockMHz == right.MemoryClockMHz &&
		left.MemoryOffsetMHz == right.MemoryOffsetMHz &&
		left.PowerLimitW == right.PowerLimitW &&
		left.FanPercent == right.FanPercent
}

func tuningIsEmpty(setting protocol.GPUOverclock) bool {
	return setting.CoreClockMHz == 0 && setting.CoreOffsetMHz == 0 &&
		setting.MemoryClockMHz == 0 && setting.MemoryOffsetMHz == 0 &&
		setting.PowerLimitW == 0 && setting.FanPercent == 0
}

func (s *Store) evaluateAlertsLocked(rig *storedRig) {
	policy := rig.Desired.Watchdog
	if !policy.Enabled || policy.MaxTemperatureC <= 0 {
		_ = s.resolveAlertLocked(rig.ID, "temperature")
	} else {
		var hottest protocol.GPU
		for _, gpu := range rig.Metrics.GPUs {
			if gpu.Temperature > hottest.Temperature {
				hottest = gpu
			}
		}
		if hottest.Temperature >= policy.MaxTemperatureC {
			_ = s.activateAlertLocked(rig.ID, "temperature", "critical",
				fmt.Sprintf("%s %s reached %.1f C (limit %.1f C)", hottest.Vendor, hottest.Name, hottest.Temperature, policy.MaxTemperatureC))
		} else {
			_ = s.resolveAlertLocked(rig.ID, "temperature")
		}
	}
	if policy.Enabled && policy.MinHashrate > 0 && rig.Metrics.Miner.Running && rig.Metrics.Miner.Hashrate < policy.MinHashrate {
		_ = s.activateAlertLocked(rig.ID, "hashrate", "warning",
			fmt.Sprintf("Hashrate %.2f %s is below minimum %.2f", rig.Metrics.Miner.Hashrate, rig.Metrics.Miner.HashrateUnit, policy.MinHashrate))
	} else {
		_ = s.resolveAlertLocked(rig.ID, "hashrate")
	}
	if rig.Desired.FlightSheetID != "" && rig.Metrics.AppliedRevision == rig.Desired.Revision && !rig.Metrics.Miner.Running {
		_ = s.activateAlertLocked(rig.ID, "miner", "critical", "Assigned miner is not running")
	} else {
		_ = s.resolveAlertLocked(rig.ID, "miner")
	}
}

func (s *Store) activateAlertLocked(rigID, alertType, severity, message string) bool {
	for index := range s.state.Alerts {
		alert := &s.state.Alerts[index]
		if alert.RigID == rigID && alert.Type == alertType && alert.Active {
			alert.Message = message
			alert.Severity = severity
			return false
		}
	}
	alert := protocol.Alert{
		ID: mustToken(10), RigID: rigID, Type: alertType,
		Severity: severity, Message: message, StartedAt: time.Now().UTC(), Active: true,
	}
	s.state.Alerts = append(s.state.Alerts, alert)
	s.recordLocked("trigger", "alert", alert.ID, map[string]any{"rig_id": rigID, "type": alertType})
	if len(s.state.Alerts) > 2000 {
		s.state.Alerts = append([]protocol.Alert(nil), s.state.Alerts[len(s.state.Alerts)-2000:]...)
	}
	return true
}

func (s *Store) resolveAlertLocked(rigID, alertType string) bool {
	now := time.Now().UTC()
	changed := false
	for index := range s.state.Alerts {
		alert := &s.state.Alerts[index]
		if alert.RigID == rigID && alert.Type == alertType && alert.Active {
			alert.Active = false
			alert.ResolvedAt = &now
			s.recordLocked("resolve", "alert", alert.ID, map[string]any{"rig_id": rigID, "type": alertType})
			changed = true
		}
	}
	return changed
}

func (s *Store) EvaluateWorkerConnectivity(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, rig := range s.state.Rigs {
		offline := rig.LastHeartbeat.IsZero() || now.Sub(rig.LastHeartbeat) >= 30*time.Second
		if offline {
			if s.activateAlertLocked(rig.ID, "offline", "warning", "Worker heartbeat is overdue") {
				changed = true
			}
		} else if s.resolveAlertLocked(rig.ID, "offline") {
			changed = true
		}
	}
	if changed {
		return s.saveLocked()
	}
	return nil
}

func (s *Store) WorkerHistory(rigID string, since, until time.Time, maxPoints int) ([]protocol.Metrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.state.Rigs[rigID]; !ok {
		return nil, ErrNotFound
	}
	if !until.After(since) {
		return nil, errors.New("history end must be after start")
	}
	history, err := s.readHistory(rigID, since, until)
	if err != nil {
		return nil, err
	}
	if maxPoints > 0 && len(history) > maxPoints {
		history = downsampleMetrics(history, maxPoints)
	}
	return history, nil
}

func (s *Store) appendMetric(rigID string, metrics protocol.Metrics) error {
	directory := filepath.Join(s.historyDir, rigID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, metrics.CollectedAt.UTC().Format("2006-01-02")+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encodeErr := encoder.Encode(metrics)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}

func (s *Store) readHistory(rigID string, since, until time.Time) ([]protocol.Metrics, error) {
	directory := filepath.Join(s.historyDir, rigID)
	result := make([]protocol.Metrics, 0)
	for day := since.UTC().Truncate(24 * time.Hour); day.Before(until); day = day.AddDate(0, 0, 1) {
		path := filepath.Join(directory, day.Format("2006-01-02")+".jsonl")
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read telemetry: %w", err)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			var metrics protocol.Metrics
			if err := json.Unmarshal(scanner.Bytes(), &metrics); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("decode telemetry %s: %w", filepath.Base(path), err)
			}
			if !metrics.CollectedAt.Before(since) && metrics.CollectedAt.Before(until) {
				result = append(result, metrics)
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("scan telemetry %s: %w", filepath.Base(path), scanErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CollectedAt.Before(result[j].CollectedAt) })
	return result, nil
}

func downsampleMetrics(history []protocol.Metrics, maximum int) []protocol.Metrics {
	if maximum <= 0 || len(history) <= maximum {
		return history
	}
	result := make([]protocol.Metrics, 0, maximum)
	for index := 0; index < maximum; index++ {
		source := index * (len(history) - 1) / (maximum - 1)
		result = append(result, history[source])
	}
	return result
}

func (s *Store) pruneHistory(now time.Time) error {
	cutoff := now.UTC().Add(-historyRetention).Truncate(24 * time.Hour)
	entries, err := os.ReadDir(s.historyDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, rigEntry := range entries {
		if !rigEntry.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.historyDir, rigEntry.Name()))
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			day, err := time.Parse("2006-01-02", strings.TrimSuffix(file.Name(), ".jsonl"))
			if err == nil && day.Before(cutoff) {
				if err := os.Remove(filepath.Join(s.historyDir, rigEntry.Name(), file.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) migrateEmbeddedHistory() error {
	if len(s.state.History) == 0 {
		return nil
	}
	for rigID, history := range s.state.History {
		for _, metrics := range history {
			if err := s.appendMetric(rigID, metrics); err != nil {
				return err
			}
			if metrics.CollectedAt.After(s.state.HistoryRecordedAt[rigID]) {
				s.state.HistoryRecordedAt[rigID] = metrics.CollectedAt
			}
		}
	}
	s.state.History = nil
	return s.saveLocked()
}

func (s *Store) ListAlerts(activeOnly bool) []protocol.Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.Alert, 0, len(s.state.Alerts))
	for index := len(s.state.Alerts) - 1; index >= 0; index-- {
		if !activeOnly || s.state.Alerts[index].Active {
			result = append(result, s.state.Alerts[index])
		}
	}
	return result
}
