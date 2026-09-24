package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"minerdash/internal/protocol"
)

func TestTelemetryHistoryRetentionPersistenceAndDownsampling(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	store, err := NewStore(statePath, "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("history-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	samples := []protocol.Metrics{
		{CollectedAt: now.Add(-33 * 24 * time.Hour), CPUUsage: 1},
		{CollectedAt: now.Add(-32*24*time.Hour + time.Hour), CPUUsage: 2},
		{CollectedAt: now.Add(-time.Hour), CPUUsage: 3},
		{CollectedAt: now, CPUUsage: 4},
	}
	for _, sample := range samples {
		if err := store.Heartbeat(identity.AgentID, sample, "192.0.2.10"); err != nil {
			t.Fatal(err)
		}
	}

	oldPath := filepath.Join(root, "telemetry", identity.AgentID, samples[0].CollectedAt.Format("2006-01-02")+".jsonl")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired telemetry file still exists: %v", err)
	}

	reloaded, err := NewStore(statePath, "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	history, err := reloaded.WorkerHistory(identity.AgentID, now.Add(-34*24*time.Hour), now.Add(time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].CPUUsage != 2 || history[1].CPUUsage != 4 {
		t.Fatalf("downsampled retained history = %#v", history)
	}
}

func TestDriverEfficiencyLearnsBestLocalDriver(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("gpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	sheet := protocol.FlightSheet{ID: "sheet", Coin: "YEC", Algorithm: "equihash192_7", DeviceType: "gpu"}
	store.state.FlightSheets[sheet.ID] = sheet
	rig := store.state.Rigs[identity.AgentID]
	rig.Desired.FlightSheetID = sheet.ID
	rig.Desired.PowerOffsetW = 20
	rig.Metrics = protocol.Metrics{GPUs: []protocol.GPU{{Index: 0, Vendor: "NVIDIA", Name: "RTX 3070", Driver: "550"}}}

	addRecord := func(driver string, hashrate float64) {
		record := protocol.DriverEfficiencyRecord{
			Vendor: "NVIDIA", Model: "RTX 3070", Driver: driver, Coin: "YEC",
			Algorithm: "equihash192_7", HashrateUnit: "Sol/s", Samples: 60,
			TotalHashrate: hashrate * 60, TotalPowerW: 120 * 60,
		}
		store.state.DriverEfficiency[driverEfficiencyKey(record)] = &record
	}
	addRecord("550", 100)
	addRecord("535", 110)

	summaries := store.driverEfficiencySummariesLocked(&rig.Rig)
	if len(summaries) != 1 {
		t.Fatalf("summary count = %d, want 1", len(summaries))
	}
	summary := summaries[0]
	if summary.Status != "better-tested" || summary.BestDriver != "535" || summary.ImprovementPercent < 9.9 {
		t.Fatalf("driver summary = %#v", summary)
	}
}

func TestDriverEfficiencyCollectsPerGPUHashrateAndCalibratedPower(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("gpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	rig := store.state.Rigs[identity.AgentID]
	rig.Desired.PowerOffsetW = 20
	metrics := protocol.Metrics{
		CollectedAt: time.Now().UTC(), MiningCoin: "YEC", MiningAlgorithm: "equihash192_7",
		Miner: protocol.MinerState{
			Running: true,
			Devices: []protocol.DeviceHashrate{{Index: 0, Kind: "GPU", Hashrate: 100, Unit: "Sol/s"}},
		},
		GPUs: []protocol.GPU{{Index: 0, Vendor: "NVIDIA", Name: "RTX 3070", Driver: "550", Power: 100}},
	}
	store.recordDriverEfficiencyLocked(identity.AgentID, metrics)
	if len(store.state.DriverEfficiency) != 1 {
		t.Fatalf("record count = %d, want 1", len(store.state.DriverEfficiency))
	}
	for _, record := range store.state.DriverEfficiency {
		if record.Samples != 1 || record.TotalHashrate != 100 || record.TotalPowerW != 120 {
			t.Fatalf("efficiency record = %#v", record)
		}
	}
}

func TestOverclockKnowledgePresetUsesMeasuredTuning(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("gpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	sheet := protocol.FlightSheet{ID: "sheet", Coin: "YEC", Algorithm: "equihash192_7", DeviceType: "gpu"}
	store.state.FlightSheets[sheet.ID] = sheet
	rig := store.state.Rigs[identity.AgentID]
	rig.Desired.FlightSheetID = sheet.ID
	rig.Metrics = protocol.Metrics{
		Miner: protocol.MinerState{Profile: "miniZ"},
		GPUs:  []protocol.GPU{{Index: 0, Vendor: "NVIDIA", Name: "RTX 3070", Driver: "550"}},
	}
	record := protocol.DriverEfficiencyRecord{
		Vendor: "NVIDIA", Model: "RTX 3070", Driver: "550", Coin: "YEC",
		Algorithm: "equihash192_7", Miner: "miniZ", HashrateUnit: "Sol/s",
		Samples: 60, TotalHashrate: 6000, TotalPowerW: 6000,
		Tuning: protocol.GPUOverclock{Selector: "0", CoreOffsetMHz: 100, MemoryOffsetMHz: 1200, PowerLimitW: 115},
	}
	store.state.DriverEfficiency[driverEfficiencyKey(record)] = &record

	presets := store.overclockKnowledgePresetsLocked(&rig.Rig)
	if len(presets) != 1 || presets[0].Tuning.MemoryOffsetMHz != 1200 || presets[0].Samples != 60 {
		t.Fatalf("knowledge presets = %#v", presets)
	}
}
