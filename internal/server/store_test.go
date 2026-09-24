package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"minerdash/internal/protocol"
)

func TestStoreAgentAndCommandLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(path, "admin-secret", "enroll-secret")
	if err != nil {
		t.Fatal(err)
	}
	if store.IsAdmin("wrong") || !store.IsAdmin("admin-secret") {
		t.Fatal("admin authentication returned an unexpected result")
	}
	if _, err := store.Enroll("rig-01", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Enroll() error = %v, want unauthorized", err)
	}
	identity, err := store.Enroll("rig-01", "enroll-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AuthenticateAgent(identity.AgentID, identity.Token); err != nil {
		t.Fatalf("AuthenticateAgent() error = %v", err)
	}
	if err := store.AuthenticateAgent(identity.AgentID, "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateAgent() error = %v, want unauthorized", err)
	}
	metrics := protocol.Metrics{CollectedAt: time.Now().UTC(), CPUUsage: 42}
	if err := store.Heartbeat(identity.AgentID, metrics, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	rigs := store.ListRigs(time.Now().UTC())
	if len(rigs) != 1 || !rigs[0].Online || rigs[0].Metrics.CPUUsage != 42 || rigs[0].IPAddress != "192.0.2.10" {
		t.Fatalf("ListRigs() = %#v", rigs)
	}
	created, err := store.AddCommand(identity.AgentID, "start", "qrl")
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.NextCommand(identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != created.ID || next.Status != "running" {
		t.Fatalf("NextCommand() = %#v", next)
	}
	if err := store.FinishCommand(identity.AgentID, created.ID, "miner failed", "test output"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextCommand(identity.AgentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("NextCommand() error = %v, want not found", err)
	}

	reloaded, err := NewStore(path, "admin-secret", "enroll-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.ListRigs(time.Now().UTC())) != 1 {
		t.Fatal("persisted rig was not loaded")
	}
	history, err := reloaded.WorkerHistory(identity.AgentID, metrics.CollectedAt.Add(-time.Minute), metrics.CollectedAt.Add(time.Minute), 2000)
	if err != nil || len(history) != 1 || history[0].CPUUsage != 42 {
		t.Fatalf("persisted history = %#v, error = %v", history, err)
	}
}

func TestStoreRejectsUnknownActions(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddCommand(identity.AgentID, "shell", "anything"); err == nil {
		t.Fatal("AddCommand() accepted an arbitrary action")
	}
}

func TestGPUDriverUpdateRequiresSupportedOnlineVendor(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("gpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	rig := store.state.Rigs[identity.AgentID]
	rig.LastHeartbeat = time.Now().UTC()
	rig.Metrics.DriverUpdateSupported = true
	rig.Metrics.GPUs = []protocol.GPU{{Index: 0, Vendor: "NVIDIA"}}
	store.mu.Unlock()
	command, err := store.AddCommandRequest(identity.AgentID, protocol.CommandRequest{
		Action:       "gpu-driver-update",
		DriverUpdate: &protocol.DriverUpdateRequest{Vendor: "nvidia", Channel: "stable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.DriverUpdate == nil || command.DriverUpdate.Vendor != "NVIDIA" {
		t.Fatalf("driver update command = %#v", command)
	}
	if _, err := store.AddCommandRequest(identity.AgentID, protocol.CommandRequest{
		Action:       "gpu-driver-update",
		DriverUpdate: &protocol.DriverUpdateRequest{Vendor: "AMD", Channel: "stable"},
	}); err == nil {
		t.Fatal("accepted driver update for a vendor not present on the rig")
	}
}

func TestEnrollmentRequiresUniqueRigNames(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enroll("CPU-Rig-01", "enroll"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enroll(" cpu-rig-01 ", "enroll"); err == nil {
		t.Fatal("Enroll() accepted a duplicate rig name")
	}
}

func TestExpiredCommandLeaseIsRedelivered(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := store.Enroll("rig", "enroll")
	created, _ := store.AddCommand(identity.AgentID, "restart", "")
	first, err := store.NextCommand(identity.AgentID)
	if err != nil || first.ID != created.ID || first.Attempt != 1 {
		t.Fatalf("first delivery = %#v, error = %v", first, err)
	}
	store.mu.Lock()
	store.state.Rigs[identity.AgentID].Commands[0].LeaseUntil = time.Now().Add(-time.Second)
	store.mu.Unlock()
	second, err := store.NextCommand(identity.AgentID)
	if err != nil || second.ID != created.ID || second.Attempt != 2 {
		t.Fatalf("redelivery = %#v, error = %v", second, err)
	}
}

func TestSmartTuneEnforcesPowerCapAndPersistsResult(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("gpu-rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.SaveOverclockProfile(protocol.OverclockProfile{
		Name: "GPU tuning", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{
			{Selector: "0", PowerLimitW: 115},
			{Selector: "*", PowerLimitW: 115},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.state.Rigs[identity.AgentID].Metrics = protocol.Metrics{
		GPUs:               []protocol.GPU{{Index: 0, Vendor: "NVIDIA"}},
		Miner:              protocol.MinerState{Running: true},
		SmartTuneSupported: true,
	}
	store.state.Rigs[identity.AgentID].Desired.OverclockProfileID = profile.ID
	store.mu.Unlock()
	if _, err := store.AddCommandRequest(identity.AgentID, protocol.CommandRequest{
		Action: "smart-tune", SmartTune: &protocol.SmartTuneRequest{Mode: "balanced", MaxPowerW: 116, MaxTemperatureC: 78},
	}); err == nil {
		t.Fatal("AddCommandRequest() accepted a limit above 115 W")
	}
	command, err := store.AddCommandRequest(identity.AgentID, protocol.CommandRequest{
		Action: "smart-tune", SmartTune: &protocol.SmartTuneRequest{Mode: "balanced", MaxPowerW: 115, MaxTemperatureC: 78},
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.Profile != profile.ID {
		t.Fatalf("command profile = %q, want %q", command.Profile, profile.ID)
	}
	if _, err := store.AddCommandRequest(identity.AgentID, protocol.CommandRequest{
		Action: "smart-tune", SmartTune: validSmartTuneRequest(),
	}); err == nil {
		t.Fatal("AddCommandRequest() accepted a duplicate active tune")
	}
	if _, err := store.NextCommand(identity.AgentID); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishCommandResult(identity.AgentID, command.ID, protocol.CommandResult{
		Output: "complete", TunedSettings: []protocol.GPUOverclock{{Selector: "0", PowerLimitW: 105}},
	}); err != nil {
		t.Fatal(err)
	}
	updated := store.ListOverclockProfiles()[0]
	if updated.Settings[0].PowerLimitW != 105 {
		t.Fatalf("power limit = %d, want 105", updated.Settings[0].PowerLimitW)
	}
	if updated.Settings[1].PowerLimitW != 0 {
		t.Fatalf("wildcard power limit = %d, want 0", updated.Settings[1].PowerLimitW)
	}
	rig := store.ListRigs(time.Now())[0]
	if rig.Desired.Revision != 1 {
		t.Fatalf("revision = %d, want 1", rig.Desired.Revision)
	}
}

func TestSmartTuneRejectsSharedProfileAndBulkStart(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.Enroll("gpu-one", "enroll")
	second, _ := store.Enroll("gpu-two", "enroll")
	store.mu.Lock()
	assignTestOverclockProfile(t, store, first.AgentID)
	store.state.Rigs[first.AgentID].Metrics = protocol.Metrics{
		GPUs: []protocol.GPU{{Index: 0, Vendor: "NVIDIA"}}, Miner: protocol.MinerState{Running: true}, SmartTuneSupported: true,
	}
	store.state.Rigs[second.AgentID].Desired.OverclockProfileID = "test-profile"
	store.mu.Unlock()
	if _, err := store.AddCommandRequest(first.AgentID, protocol.CommandRequest{
		Action: "smart-tune", SmartTune: validSmartTuneRequest(),
	}); err == nil {
		t.Fatal("AddCommandRequest() accepted a shared overclock profile")
	}
	if _, err := store.AddCommands([]string{first.AgentID}, "smart-tune", ""); err == nil {
		t.Fatal("AddCommands() accepted bulk smart tune")
	}
}

func TestNVIDIAOverclockProfileRejectsPowerAbove115W(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveOverclockProfile(protocol.OverclockProfile{
		Name: "unsafe", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{{Selector: "0", PowerLimitW: 116}},
	}); err == nil {
		t.Fatal("SaveOverclockProfile() accepted an NVIDIA limit above 115 W")
	}
}

func TestSmartTuneRejectsUnsafeAndIneligibleRequests(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Store, string)
		req   protocol.CommandRequest
	}{
		{
			name: "missing settings",
			req:  protocol.CommandRequest{Action: "smart-tune"},
		},
		{
			name: "no GPUs",
			setup: func(store *Store, rigID string) {
				assignTestOverclockProfile(t, store, rigID)
				store.state.Rigs[rigID].Metrics.Miner.Running = true
				store.state.Rigs[rigID].Metrics.SmartTuneSupported = true
			},
			req: protocol.CommandRequest{Action: "smart-tune", SmartTune: validSmartTuneRequest()},
		},
		{
			name: "miner stopped",
			setup: func(store *Store, rigID string) {
				assignTestOverclockProfile(t, store, rigID)
				store.state.Rigs[rigID].Metrics.GPUs = []protocol.GPU{{Index: 0, Vendor: "NVIDIA"}}
				store.state.Rigs[rigID].Metrics.SmartTuneSupported = true
			},
			req: protocol.CommandRequest{Action: "smart-tune", SmartTune: validSmartTuneRequest()},
		},
		{
			name: "no profile",
			setup: func(store *Store, rigID string) {
				store.state.Rigs[rigID].Metrics.Miner.Running = true
				store.state.Rigs[rigID].Metrics.GPUs = []protocol.GPU{{Index: 0, Vendor: "NVIDIA"}}
				store.state.Rigs[rigID].Metrics.SmartTuneSupported = true
			},
			req: protocol.CommandRequest{Action: "smart-tune", SmartTune: validSmartTuneRequest()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
			if err != nil {
				t.Fatal(err)
			}
			identity, err := store.Enroll("gpu-rig", "enroll")
			if err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			if test.setup != nil {
				test.setup(store, identity.AgentID)
			}
			store.mu.Unlock()
			if _, err := store.AddCommandRequest(identity.AgentID, test.req); err == nil {
				t.Fatal("AddCommandRequest() accepted an ineligible request")
			}
		})
	}
}

func validSmartTuneRequest() *protocol.SmartTuneRequest {
	return &protocol.SmartTuneRequest{Mode: "balanced", MaxPowerW: 115, MaxTemperatureC: 78}
}

func assignTestOverclockProfile(t *testing.T, store *Store, rigID string) {
	t.Helper()
	profile := protocol.OverclockProfile{
		ID: "test-profile", Name: "GPU tuning", Vendor: "NVIDIA",
		Settings: []protocol.GPUOverclock{{Selector: "0", PowerLimitW: 115}},
	}
	store.state.OverclockProfiles[profile.ID] = profile
	store.state.Rigs[rigID].Desired.OverclockProfileID = profile.ID
}

func TestFailedPersistenceRollsBackMemory(t *testing.T) {
	root := t.TempDir()
	blockedDirectory := filepath.Join(root, "blocked")
	if err := os.WriteFile(blockedDirectory, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(blockedDirectory, "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enroll("must-not-remain", "enroll"); err == nil {
		t.Fatal("Enroll() succeeded with an invalid data path")
	}
	if rigs := store.ListRigs(time.Now()); len(rigs) != 0 {
		t.Fatalf("failed enrollment remained in memory: %#v", rigs)
	}
}

func TestOneTimeEnrollmentGrantIsBoundAndConsumed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(path, "admin", "shared-enrollment")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateEnrollmentGrant("paired-rig", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(path, "admin", "shared-enrollment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Enroll("different-rig", token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("grant accepted for the wrong rig: %v", err)
	}
	if _, err := reloaded.Enroll("paired-rig", token); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Enroll("paired-rig-two", token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("consumed grant was accepted again: %v", err)
	}
}
