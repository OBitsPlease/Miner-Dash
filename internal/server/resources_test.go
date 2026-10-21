package server

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"minerdash/internal/protocol"
)

func TestValidateFlightSheetMinerReadyAllowsAutomaticTReX(t *testing.T) {
	err := validateFlightSheetMinerReady(protocol.MinerDefinition{
		ID: "trex", CatalogID: "trex", Name: "T-Rex", Version: "package-required", ManagedBinary: true, BinaryName: "t-rex",
	})
	if err != nil {
		t.Fatalf("automatic T-Rex should be installable, got %v", err)
	}
}

func TestValidateFlightSheetMinerReadyExplainsMissingManualPackage(t *testing.T) {
	err := validateFlightSheetMinerReady(protocol.MinerDefinition{
		ID: "manual", CatalogID: "manual", Name: "Manual Miner", Version: "package-required", ManagedBinary: true, BinaryName: "manual",
	})
	if err == nil || !strings.Contains(err.Error(), "Manual Miner is not installed") ||
		!strings.Contains(err.Error(), "Choose an installed miner") {
		t.Fatalf("expected actionable package error, got %v", err)
	}
}

func TestResourceAssignmentResolutionAndAlerts(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}

	farm, err := store.SaveFarm(protocol.Farm{Name: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := store.SaveWallet(protocol.Wallet{Name: "QRL", Coin: "QRL", Address: "Q010203"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.SavePool(protocol.Pool{Name: "Pool", URL: "stratum+tcp://pool.invalid:3333", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.SaveMiner(protocol.MinerDefinition{Name: "CPU Miner", Profile: "qrl", Algorithm: "QRandomX"})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := store.SaveFlightSheet(protocol.FlightSheet{Name: "QRL CPU", WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("rig-01", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	rig, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{
		Name: "renamed-rig", FarmID: farm.ID, FlightSheetID: sheet.ID,
		EstimatedPowerW: 125, PowerOffsetW: 42.5,
		Watchdog: protocol.WatchdogPolicy{Enabled: true, MaxTemperatureC: 75},
		Autofan:  protocol.AutofanPolicy{Enabled: true, TargetTemperatureC: 60, MinimumFanPercent: 35, MaximumFanPercent: 90},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rig.Desired.Revision != 1 {
		t.Fatalf("revision = %d, want 1", rig.Desired.Revision)
	}
	if rig.Name != "renamed-rig" {
		t.Fatalf("worker name = %q, want renamed-rig", rig.Name)
	}
	if rig.Desired.EstimatedPowerW != 125 || rig.Desired.PowerOffsetW != 42.5 {
		t.Fatalf("power calibration = %#v", rig.Desired)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}

	if resolved.Miner.Profile != "qrl" || resolved.Wallet.Address != wallet.Address || resolved.Pool.URL != pool.URL {
		t.Fatalf("resolved configuration = %#v", resolved)
	}
	wallet.Address = "QUPDATED"
	if _, err := store.SaveWallet(wallet); err != nil {
		t.Fatal(err)
	}
	updated, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != resolved.Revision+1 || updated.Wallet.Address != "QUPDATED" {
		t.Fatalf("updated configuration = %#v", updated)
	}
	now := time.Now().UTC()
	metrics := protocol.Metrics{
		CollectedAt: now,
		GPUs:        []protocol.GPU{{Vendor: "NVIDIA", Name: "GPU", Temperature: 80}},
	}
	if err := store.Heartbeat(identity.AgentID, metrics, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if alerts := store.ListAlerts(true); len(alerts) != 1 || alerts[0].Type != "temperature" {
		t.Fatalf("active alerts = %#v", alerts)
	}
	history, err := store.WorkerHistory(identity.AgentID, now.Add(-time.Minute), now.Add(time.Minute), 2000)
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %#v, error = %v", history, err)
	}
	metrics.CollectedAt = now.Add(time.Minute)
	metrics.GPUs[0].Temperature = 60
	if err := store.Heartbeat(identity.AgentID, metrics, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if alerts := store.ListAlerts(true); len(alerts) != 0 {
		t.Fatalf("active alerts after recovery = %#v", alerts)
	}
}

func TestResolvedWorkerConfigurationEnforcesControllerGPUPowerPolicy(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("gpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.SaveOverclockProfile(protocol.OverclockProfile{
		Name: "high power", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{{Selector: "0", PowerLimitW: 180}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{OverclockProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.state.Rigs[identity.AgentID].Metrics.GPUs = []protocol.GPU{
		{Index: 0, Vendor: "NVIDIA"},
		{Index: 1, Vendor: "NVIDIA"},
	}
	store.mu.Unlock()
	if err := store.SetGPUPowerPolicy(protocol.GPUPowerPolicy{MaxPowerW: 115}); err != nil {
		t.Fatal(err)
	}

	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.GPUPowerLimitW != 115 {
		t.Fatalf("resolved cap = %d W, want 115 W", resolved.GPUPowerLimitW)
	}
	want := []protocol.GPUOverclock{
		{Selector: "*", PowerLimitW: 115},
		{Selector: "0", PowerLimitW: 115},
	}
	if !reflect.DeepEqual(resolved.Overclock.Settings, want) {
		t.Fatalf("resolved overclock settings = %#v, want %#v", resolved.Overclock.Settings, want)
	}
}

func TestSaveFarmElectricityRate(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	farm, err := store.SaveFarm(protocol.Farm{Name: "Main", ElectricityRateUSDPerKWh: 0.15})
	if err != nil {
		t.Fatal(err)
	}
	if farm.ElectricityRateUSDPerKWh != 0.15 {
		t.Fatalf("electricity rate = %v, want 0.15", farm.ElectricityRateUSDPerKWh)
	}
	farm.ElectricityRateUSDPerKWh = -0.01
	if _, err := store.SaveFarm(farm); err == nil {
		t.Fatal("expected negative electricity rate to be rejected")
	}
}

func TestFreshInstallStartsWithGPUAndCPUFarms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(path, "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	farms := store.ListFarms()
	if len(farms) != 2 || farms[0].Name != "CPU Farm" || farms[1].Name != "GPU Farm" {
		t.Fatalf("fresh install farms = %#v, want CPU Farm and GPU Farm", farms)
	}
	for _, farm := range farms {
		if err := store.DeleteResource("farms", farm.ID); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := NewStore(path, "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if farms := reloaded.ListFarms(); len(farms) != 0 {
		t.Fatalf("existing empty farm list was backfilled: %#v", farms)
	}
}

func TestWorkerAssignmentRejectsInvalidPowerCalibration(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("power-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{EstimatedPowerW: -1}); err == nil {
		t.Fatal("AssignWorker() accepted a negative power estimate")
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{PowerOffsetW: 100001}); err == nil {
		t.Fatal("AssignWorker() accepted an excessive power offset")
	}
}

func TestFlightSheetRequiresExistingReferences(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveFlightSheet(protocol.FlightSheet{Name: "invalid", WalletID: "missing", PoolID: "missing", MinerID: "missing"}); err == nil {
		t.Fatal("SaveFlightSheet() accepted missing references")
	}
}

func TestEpicFlightSheetResolvesCPUFallback(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("cpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	epicWallet, _ := store.SaveWallet(protocol.Wallet{Name: "Epic", Coin: "EPIC", Address: "solo"})
	epicPool, _ := store.SavePool(protocol.Pool{Name: "Epic Stratum", URL: "stratum+tcp://192.0.2.79:3416"})
	epicMiner, err := store.InstallCatalogMiner("epic-miner")
	if err != nil {
		t.Fatal(err)
	}
	qrlWallet, _ := store.SaveWallet(protocol.Wallet{Name: "QRL", Coin: "QRL", Address: "Q010203"})
	qrlPool, _ := store.SavePool(protocol.Pool{Name: "QRL Pool", URL: "stratum+tcp://pool.invalid:3333"})
	qrlMiner, _ := store.SaveMiner(protocol.MinerDefinition{Name: "QRL Miner", Profile: "qrl", Algorithm: "qrandomx"})
	fallback, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "QRL fallback", DeviceType: "CPU", Coin: "QRL", Algorithm: "qrandomx",
		WalletID: qrlWallet.ID, PoolID: qrlPool.ID, MinerID: qrlMiner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	primary, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "Epic primary", DeviceType: "CPU", Coin: "EPIC", Algorithm: "randomx",
		WalletID: epicWallet.ID, PoolID: epicPool.ID, MinerID: epicMiner.ID,
		FallbackFlightSheetID: fallback.ID, FailoverAfterSeconds: 10, ReturnAfterSeconds: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{FlightSheetID: primary.ID}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Fallback == nil || resolved.Fallback.Coin != "QRL" ||
		resolved.Fallback.Miner.Profile != "qrl" || resolved.Fallback.Pool.URL != qrlPool.URL {
		t.Fatalf("resolved fallback = %#v", resolved.Fallback)
	}
	if resolved.FailoverAfterSeconds != 10 || resolved.ReturnAfterSeconds != 15 {
		t.Fatalf("resolved failover delays = %d/%d", resolved.FailoverAfterSeconds, resolved.ReturnAfterSeconds)
	}
}

func TestFlightSheetRejectsInvalidFallback(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	wallet, _ := store.SaveWallet(protocol.Wallet{Name: "Wallet", Coin: "TEST", Address: "address"})
	pool, _ := store.SavePool(protocol.Pool{Name: "Pool", URL: "stratum+tcp://pool.invalid:3333"})
	miner, _ := store.SaveMiner(protocol.MinerDefinition{Name: "Miner", Profile: "miner", Algorithm: "test"})
	fallback, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "Fallback", DeviceType: "CPU", Coin: "TEST", Algorithm: "test",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.SaveFlightSheet(protocol.FlightSheet{
		Name: "Unsupported primary", DeviceType: "CPU", Coin: "TEST", Algorithm: "test",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID, FallbackFlightSheetID: fallback.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "Epic CPU Miner") {
		t.Fatalf("invalid fallback error = %v", err)
	}
}

func TestFlightSheetDetailedConfigurationResolvesOverrides(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("rig-default", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := store.SaveWallet(protocol.Wallet{Name: "Wallet", Coin: "TEST", Address: "wallet-address"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.SavePool(protocol.Pool{Name: "Pool", URL: "stratum+tcp://pool.invalid:3333", Password: "pool-pass"})
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.SaveMiner(protocol.MinerDefinition{
		Name: "Miner", Algorithm: "multi", Profile: "miner",
		DefaultArguments: []string{"--pool", "{POOL}", "--user", "{WALLET}.{WORKER}", "--pass", "{PASSWORD}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "Detailed", DeviceType: "GPU", Coin: "TEST", Algorithm: "testhash",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID,
		PoolURLOverride: "stratum+ssl://override.invalid:4444", PoolPasswordOverride: "override-pass",
		WalletTemplate: "solo:{WALLET}.{WORKER}", WorkerName: "fixed-worker",
		ExtraArguments: []string{"--extra", "value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{FlightSheetID: sheet.ID}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Pool.URL != "stratum+ssl://override.invalid:4444" || resolved.Pool.Password != "override-pass" {
		t.Fatalf("resolved pool = %#v", resolved.Pool)
	}
	if resolved.MiningUser != "solo:wallet-address.fixed-worker" || resolved.WorkerName != "fixed-worker" {
		t.Fatalf("resolved mining identity = %q / %q", resolved.MiningUser, resolved.WorkerName)
	}
	if got := strings.Join(resolved.Miner.DefaultArguments, " "); !strings.Contains(got, "--user {WALLET}") || strings.Contains(got, "{WALLET}.{WORKER}") {
		t.Fatalf("resolved default arguments = %q", got)
	}
	if got := strings.Join(resolved.Miner.ExtraArguments, " "); got != "--extra value" {
		t.Fatalf("resolved extra arguments = %q", got)
	}
}

func TestTReXVeilFlightSheetUsesVeilProtocolAlgorithm(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}

	identity, err := store.Enroll("veil-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := store.SaveWallet(protocol.Wallet{Name: "VEIL", Coin: "VEIL", Address: "veil-wallet"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.SavePool(protocol.Pool{Name: "VEIL Pool", URL: "stratum+tcp://veil.invalid:3334", Password: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.SaveMiner(protocol.MinerDefinition{
		CatalogID: "trex", Name: "T-Rex", Algorithm: "multi", Profile: "trex",
		DefaultArguments: []string{"-o", "{POOL}", "-u", "{WALLET}.{WORKER}", "-p", "{PASSWORD}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "VEIL", DeviceType: "GPU", Coin: "VEIL", Algorithm: "progpow",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Algorithm != "progpow-veil" {
		t.Fatalf("saved VEIL algorithm = %q", sheet.Algorithm)
	}
	if _, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "Invalid VEIL", DeviceType: "GPU", Coin: "VEIL", Algorithm: "kawpow",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID,
	}); err == nil || !strings.Contains(err.Error(), "requires algorithm progpow-veil") {
		t.Fatalf("invalid T-Rex VEIL algorithm error = %v", err)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{FlightSheetID: sheet.ID}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Miner.Algorithm != "progpow-veil" {
		t.Fatalf("resolved VEIL algorithm = %q", resolved.Miner.Algorithm)
	}
	if got := strings.Join(resolved.Miner.ExtraArguments, " "); got != "-a progpow-veil" {
		t.Fatalf("resolved T-Rex arguments = %q", got)
	}
}

func TestVeilCPUFlightSheetRequiresVeilMiner(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := store.Enroll("veil-cpu-rig", "enroll")
	wallet, _ := store.SaveWallet(protocol.Wallet{Name: "VEIL", Coin: "VEIL", Address: "bv-wallet"})
	pool, _ := store.SavePool(protocol.Pool{Name: "VEIL RandomX", URL: "stratum+tcp://veil.invalid:3335", Password: "x"})
	stock, _ := store.InstallCatalogMiner("xmrig")
	if _, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "Invalid VEIL CPU", DeviceType: "CPU", Coin: "VEIL", Algorithm: "rx/veil",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: stock.ID,
	}); err == nil || !strings.Contains(err.Error(), "Veil CPU Miner") {
		t.Fatalf("stock XMRig VEIL error = %v", err)
	}

	veilMiner, _ := store.InstallCatalogMiner("veil-miner-cpu")
	veilMiner.BinarySHA256 = strings.Repeat("a", 64)
	veilMiner, err = store.SaveMiner(veilMiner)
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "VEIL CPU", DeviceType: "CPU", Coin: "VEIL", Algorithm: "RandomX",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: veilMiner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Algorithm != "rx/veil" {
		t.Fatalf("saved VEIL CPU algorithm = %q", sheet.Algorithm)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{FlightSheetID: sheet.ID}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Miner.CatalogID != "veil-miner-cpu" || resolved.Miner.StatsType != "xmrig" {
		t.Fatalf("resolved VEIL CPU miner = %#v", resolved.Miner)
	}
	if got := strings.Join(resolved.Miner.ExtraArguments, " "); got != "-a rx/veil" {
		t.Fatalf("resolved VEIL CPU arguments = %q", got)
	}
}

func TestSRBFlightSheetUsesDetailedWalletTemplate(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := store.Enroll("rig-default", "enroll")
	wallet, _ := store.SaveWallet(protocol.Wallet{Name: "Wallet", Coin: "TEST", Address: "wallet-address"})
	pool, _ := store.SavePool(protocol.Pool{Name: "Pool", URL: "stratum+tcp://pool.invalid:3333", Password: "x"})
	miner, _ := store.SaveMiner(protocol.MinerDefinition{
		CatalogID: "srbminer-multi", Name: "SRBMiner-MULTI", Algorithm: "multi",
		ManagedBinary: true, BinaryName: "SRBMiner-MULTI",
	})
	sheet, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "Detailed SRB", DeviceType: "CPU", Coin: "TEST", Algorithm: "randomx",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID,
		WalletTemplate: "solo:{WALLET}.{WORKER}", WorkerName: "fixed-worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{FlightSheetID: sheet.ID}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.MiningUser != "solo:wallet-address.fixed-worker" {
		t.Fatalf("resolved mining user = %q", resolved.MiningUser)
	}
	if got := strings.Join(resolved.Miner.DefaultArguments, " "); !strings.Contains(got, "--wallet {WALLET}") {
		t.Fatalf("resolved SRB arguments = %q", got)
	}
}

func TestMiniZFlightSheetFormatsEquihashParameters(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("rig-miniz", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := store.SaveWallet(protocol.Wallet{Name: "YEC", Coin: "YEC", Address: "wallet-address"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := store.SavePool(protocol.Pool{Name: "YEC pool", URL: "stratum+tcp://pool.invalid:3053"})
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.SaveMiner(protocol.MinerDefinition{
		CatalogID: "miniz", Name: "miniZ", Algorithm: "equihash192_7",
		ManagedBinary: true, BinaryName: "miniZ",
	})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := store.SaveFlightSheet(protocol.FlightSheet{
		Name: "YEC solo", DeviceType: "GPU", Coin: "YEC", Algorithm: "192.7",
		WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{FlightSheetID: sheet.ID}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Miner.ExtraArguments; !reflect.DeepEqual(got, []string{"--par", "192,7"}) {
		t.Fatalf("miniZ arguments = %#v, want --par 192,7", got)
	}
}

func TestWorkerRenameRequiresUniqueName(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.Enroll("CPU-Rig-01", "enroll")
	second, _ := store.Enroll("CPU-Rig-02", "enroll")
	if _, err := store.AssignWorker(second.AgentID, protocol.WorkerAssignment{Name: "cpu-rig-01"}); err == nil {
		t.Fatal("AssignWorker() accepted a duplicate worker name")
	}
	rigs := store.ListRigs(time.Now())
	for _, rig := range rigs {
		if rig.ID == first.AgentID && rig.Name != "CPU-Rig-01" {
			t.Fatalf("first worker name changed to %q", rig.Name)
		}
		if rig.ID == second.AgentID && rig.Name != "CPU-Rig-02" {
			t.Fatalf("second worker name changed to %q", rig.Name)
		}
	}
}

func TestFarmAssignmentDoesNotRestartMinerForDisabledPolicyDefaults(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	farm, err := store.SaveFarm(protocol.Farm{Name: "CPU"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("CPU-Rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	rig, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{
		FarmID: farm.ID,
		Watchdog: protocol.WatchdogPolicy{
			RestartAfterSeconds: 60,
			RebootAfterFailures: 3,
		},
		Autofan: protocol.AutofanPolicy{
			MinimumFanPercent: 35,
			MaximumFanPercent: 100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rig.Desired.Revision != 0 {
		t.Fatalf("farm-only assignment revision = %d, want 0", rig.Desired.Revision)
	}
	if rig.Desired.Watchdog != (protocol.WatchdogPolicy{}) || rig.Desired.Autofan != (protocol.AutofanPolicy{}) {
		t.Fatalf("disabled policies were not normalized: watchdog=%#v autofan=%#v", rig.Desired.Watchdog, rig.Desired.Autofan)
	}
}

func TestGPUAdjustmentProfilePersistsEveryControlAndBumpsAssignedWorker(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("GPU-Rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.SaveOverclockProfile(protocol.OverclockProfile{
		Name: "GPU-Rig GPU tuning", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{{
			Selector: "0", PowerLimitW: 110, FanPercent: 70,
			CoreOffsetMHz: -75, MemoryOffsetMHz: 125,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rig, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{
		OverclockProfileID: profile.ID,
		EstimatedPowerW:    825,
		PowerOffsetW:       35,
		Watchdog: protocol.WatchdogPolicy{
			Enabled: true, MaxTemperatureC: 78, MinHashrate: 100,
			RestartAfterSeconds: 30, RebootAfterFailures: 4,
		},
		Autofan: protocol.AutofanPolicy{
			Enabled: true, TargetTemperatureC: 65,
			MinimumFanPercent: 35, MaximumFanPercent: 90,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if rig.Desired.Revision != 1 || resolved.Overclock.ID != profile.ID ||
		!reflect.DeepEqual(resolved.Overclock.Settings, profile.Settings) {
		t.Fatalf("resolved GPU settings = %#v; rig revision = %d", resolved.Overclock, rig.Desired.Revision)
	}
	if rig.Desired.EstimatedPowerW != 825 || rig.Desired.PowerOffsetW != 35 ||
		resolved.Watchdog.RestartAfterSeconds != 30 || resolved.Autofan.MaximumFanPercent != 90 {
		t.Fatalf("worker adjustment fields were not preserved: rig=%#v resolved=%#v", rig.Desired, resolved)
	}
}

func TestGPUAdjustmentProfileRejectsConflictingClockModes(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	tests := []protocol.GPUOverclock{
		{Selector: "0", CoreClockMHz: 1410, CoreOffsetMHz: 50},
		{Selector: "0", MemoryClockMHz: 5001, MemoryOffsetMHz: 100},
	}
	for _, setting := range tests {
		if _, err := store.SaveOverclockProfile(protocol.OverclockProfile{
			Name: "conflict", Vendor: "NVIDIA", Settings: []protocol.GPUOverclock{setting},
		}); err == nil {
			t.Fatalf("SaveOverclockProfile accepted conflicting settings: %#v", setting)
		}
	}
}

func TestPoolDashboardURLValidation(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := store.SavePool(protocol.Pool{
		Name:         "Pool",
		URL:          "stratum+tcp://pool.invalid:3333",
		DashboardURL: "https://pool.invalid/miners/{WALLET}?worker={WORKER}&coin={COIN}",
	})
	if err != nil {
		t.Fatalf("SavePool() rejected valid dashboard URL: %v", err)
	}
	if valid.DashboardURL == "" {
		t.Fatal("SavePool() did not preserve dashboard URL")
	}
	valid.DashboardURL = "https://pool.invalid/workers/{WALLET}/{WORKER}"
	if _, err := store.SavePool(valid); err != nil {
		t.Fatalf("SavePool() rejected dashboard-only update: %v", err)
	}
	if _, err := store.SavePool(protocol.Pool{
		Name:         "Unsafe",
		URL:          "stratum+tcp://pool.invalid:3333",
		DashboardURL: "javascript:alert(1)",
	}); err == nil {
		t.Fatal("SavePool() accepted unsafe dashboard URL")
	}
}

func TestSRBSingleWorkloadArguments(t *testing.T) {
	pool := protocol.Pool{URL: "stratum+tcp://pool.invalid:3333", Password: "x"}
	wallet := protocol.Wallet{Address: "Q010203"}
	tests := []struct {
		name       string
		deviceType string
		lastFlag   string
	}{
		{name: "CPU only", deviceType: "CPU", lastFlag: "--disable-gpu"},
		{name: "GPU only", deviceType: "GPU", lastFlag: "--disable-cpu"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := srbSingleWorkloadArguments(test.deviceType, "randomx", pool, wallet, "")
			if len(got) != 9 {
				t.Fatalf("srbSingleWorkloadArguments() = %v", got)
			}
			if got[0] != "--algorithm" || got[1] != "randomx" || got[5] != "Q010203.{WORKER}" || got[8] != test.lastFlag {
				t.Fatalf("srbSingleWorkloadArguments() = %v", got)
			}
		})
	}
}

func TestScheduleRunsOnlyOncePerMinute(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("rig-01", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 14, 14, 30, 0, 0, time.Local)
	_, err = store.SaveSchedule(protocol.Schedule{
		Name: "restart", Enabled: true, Days: []int{int(now.Weekday())},
		Time: "14:30", Action: "restart", RigIDs: []string{identity.AgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RunSchedulesAt(now); err != nil {
		t.Fatal(err)
	}
	if err := store.RunSchedulesAt(now.Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	command, err := store.NextCommand(identity.AgentID)
	if err != nil || command.Action != "restart" {
		t.Fatalf("scheduled command = %#v, error = %v", command, err)
	}
	if _, err := store.NextCommand(identity.AgentID); err == nil {
		t.Fatal("schedule ran more than once in the same minute")
	}
}
