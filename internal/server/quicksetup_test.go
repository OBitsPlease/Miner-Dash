package server

import (
	"path/filepath"
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

func TestQuickFlightSheetCreatesAndAppliesCompleteSetup(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("cpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.SaveQuickFlightSheet(protocol.QuickFlightSheetInput{
		Name: "QRL CPU", DeviceType: "CPU", Coin: "qrl",
		WalletAddress: "Q010203", PoolURL: "stratum+tcp://pool.invalid:3333",
		PoolPassword: "x", CatalogID: "xmrig", Algorithm: "qrandomx",
		WalletTemplate: "%WAL%.%WORKER_NAME%",
		RigIDs:         []string{identity.AgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FlightSheet.DeviceType != "CPU" || result.FlightSheet.Algorithm != "qrandomx" || !result.MinerReady || result.Assigned != 1 {
		t.Fatalf("quick setup result = %#v", result)
	}
	if len(store.ListWallets()) != 1 || len(store.ListPools()) != 1 || len(store.ListMiners()) != 1 || len(store.ListFlightSheets()) != 1 {
		t.Fatal("quick setup did not create every required resource")
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Wallet.Address != "Q010203" || resolved.Pool.URL != "stratum+tcp://pool.invalid:3333" || resolved.Miner.Profile != "xmrig" {
		t.Fatalf("resolved quick setup = %#v", resolved)
	}
	if resolved.MiningUser != "Q010203.cpu-rig" {
		t.Fatalf("resolved mining user = %q", resolved.MiningUser)
	}
	if len(resolved.Miner.ExtraArguments) != 2 || resolved.Miner.ExtraArguments[0] != "-a" || resolved.Miner.ExtraArguments[1] != "qrandomx" {
		t.Fatalf("algorithm arguments = %#v", resolved.Miner.ExtraArguments)
	}
}

func TestQuickFlightSheetReusesWalletPoolAndMiner(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	input := protocol.QuickFlightSheetInput{
		Name: "First", DeviceType: "GPU", Coin: "ZCL", WalletAddress: "t1address",
		PoolURL: "stratum+ssl://pool.invalid:4444", CatalogID: "rigel", Algorithm: "equihash192_7",
	}
	if _, err := store.SaveQuickFlightSheet(input); err != nil {
		t.Fatal(err)
	}
	input.Name = "Second"
	if _, err := store.SaveQuickFlightSheet(input); err != nil {
		t.Fatal(err)
	}
	if len(store.ListWallets()) != 1 || len(store.ListPools()) != 1 || len(store.ListMiners()) != 1 || len(store.ListFlightSheets()) != 2 {
		t.Fatalf("resources were duplicated: wallets=%d pools=%d miners=%d sheets=%d", len(store.ListWallets()), len(store.ListPools()), len(store.ListMiners()), len(store.ListFlightSheets()))
	}
}

func TestQuickFlightSheetBuildsSingleSRBCPUWorkload(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("cpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.SaveQuickFlightSheet(protocol.QuickFlightSheetInput{
		Name: "QRL", DeviceType: "CPU", Coin: "QRL", WalletAddress: "qrl-wallet",
		PoolURL: "stratum+tcp://qrl.pool.invalid:3333", PoolPassword: "cpu-pass",
		CatalogID: "srbminer-multi", Algorithm: "qrandomx", RigIDs: []string{identity.AgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(resolved.Miner.DefaultArguments, " ")
	for _, expected := range []string{
		"--algorithm qrandomx", "--pool stratum+tcp://qrl.pool.invalid:3333",
		"--wallet qrl-wallet.{WORKER}", "--password cpu-pass", "--disable-gpu",
		"--api-enable", "--api-port 21550", "--api-rig-name {WORKER}",
	} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("single CPU miner arguments %q do not contain %q", arguments, expected)
		}
	}
	if len(resolved.Miner.ExtraArguments) != 0 {
		t.Fatalf("single CPU extra arguments = %#v, want none", resolved.Miner.ExtraArguments)
	}
}

func TestQuickFlightSheetBuildsSRBCPUAndGPUDualWorkload(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("hybrid-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.SaveQuickFlightSheet(protocol.QuickFlightSheetInput{
		Name: "Hybrid", DeviceType: "GPU", Coin: "RVN", WalletAddress: "rvn-wallet",
		PoolURL: "stratum+tcp://rvn.pool.invalid:3333", PoolPassword: "gpu-pass",
		CatalogID: "srbminer-multi", Algorithm: "kawpow", RigIDs: []string{identity.AgentID},
		SecondaryDeviceType: "CPU", SecondaryCoin: "XMR", SecondaryWalletAddress: "xmr-wallet",
		SecondaryPoolURL: "stratum+ssl://xmr.pool.invalid:4444", SecondaryPoolPassword: "cpu-pass",
		SecondaryAlgorithm: "randomx",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FlightSheet.SecondaryCoin != "XMR" || result.FlightSheet.SecondaryDeviceType != "CPU" {
		t.Fatalf("dual flight sheet = %#v", result.FlightSheet)
	}
	resolved, err := store.ResolvedWorkerConfiguration(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(resolved.Miner.DefaultArguments, " ")
	for _, expected := range []string{
		"--algorithm-gpu kawpow", "--pool-gpu stratum+tcp://rvn.pool.invalid:3333",
		"--wallet-gpu rvn-wallet.{WORKER}", "--algorithm-cpu randomx",
		"--pool-cpu stratum+ssl://xmr.pool.invalid:4444", "--wallet-cpu xmr-wallet.{WORKER}",
	} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("dual miner arguments %q do not contain %q", arguments, expected)
		}
	}
}

func TestQuickFlightSheetRejectsDualWorkloadForUnsupportedMiner(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.SaveQuickFlightSheet(protocol.QuickFlightSheetInput{
		Name: "Invalid dual", DeviceType: "GPU", Coin: "RVN", WalletAddress: "rvn-wallet",
		PoolURL: "stratum+tcp://rvn.pool.invalid:3333", CatalogID: "rigel", Algorithm: "kawpow",
		SecondaryDeviceType: "CPU", SecondaryCoin: "XMR", SecondaryWalletAddress: "xmr-wallet",
		SecondaryPoolURL: "stratum+tcp://xmr.pool.invalid:4444", SecondaryAlgorithm: "randomx",
	})
	if err == nil {
		t.Fatal("SaveQuickFlightSheet() accepted dual workloads for a non-SRB miner")
	}
}
