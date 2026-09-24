package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"minerdash/internal/protocol"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
)

type storedRig struct {
	protocol.Rig
	TokenHash string             `json:"token_hash"`
	Commands  []protocol.Command `json:"commands"`
}

type enrollmentGrant struct {
	RigName   string    `json:"rig_name"`
	ExpiresAt time.Time `json:"expires_at"`
}

type state struct {
	Rigs              map[string]*storedRig                       `json:"rigs"`
	Farms             map[string]protocol.Farm                    `json:"farms"`
	Wallets           map[string]protocol.Wallet                  `json:"wallets"`
	Pools             map[string]protocol.Pool                    `json:"pools"`
	Miners            map[string]protocol.MinerDefinition         `json:"miners"`
	FlightSheets      map[string]protocol.FlightSheet             `json:"flight_sheets"`
	OverclockProfiles map[string]protocol.OverclockProfile        `json:"overclock_profiles"`
	Activities        []protocol.Activity                         `json:"activities"`
	History           map[string][]protocol.Metrics               `json:"history,omitempty"`
	HistoryRecordedAt map[string]time.Time                        `json:"history_recorded_at,omitempty"`
	DriverEfficiency  map[string]*protocol.DriverEfficiencyRecord `json:"driver_efficiency,omitempty"`
	Alerts            []protocol.Alert                            `json:"alerts"`
	Schedules         map[string]protocol.Schedule                `json:"schedules"`
	Users             map[string]*storedUser                      `json:"users"`
	EnrollmentGrants  map[string]enrollmentGrant                  `json:"enrollment_grants,omitempty"`
}

type Store struct {
	mu              sync.RWMutex
	path            string
	packageDir      string
	agentReleaseDir string
	historyDir      string
	adminHash       string
	recoveryHash    string
	recoveryCode    string
	enrollmentHash  string
	enrollmentToken string
	tlsFingerprint  string
	lastSaved       []byte
	state           state
}

func NewStore(path, adminToken, enrollmentToken string) (*Store, error) {
	s := &Store{
		path:            path,
		packageDir:      filepath.Join(filepath.Dir(path), "packages"),
		agentReleaseDir: filepath.Join(filepath.Dir(path), "agent-releases"),
		historyDir:      filepath.Join(filepath.Dir(path), "telemetry"),
		adminHash:       hashToken(adminToken),
		enrollmentHash:  hashToken(enrollmentToken),
		enrollmentToken: enrollmentToken,
		state:           newState(),
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.lastSaved, _ = json.Marshal(s.state)
		return s, nil
	}

	if err != nil {
		return nil, err
	}
	var loaded state
	if err := json.Unmarshal(data, &loaded); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	s.state = loaded
	if s.state.Rigs == nil {
		s.state.Rigs = make(map[string]*storedRig)
	}
	s.initializeMaps()
	s.lastSaved = append([]byte(nil), data...)
	if err := s.migrateEmbeddedHistory(); err != nil {
		return nil, fmt.Errorf("migrate telemetry history: %w", err)
	}
	return s, nil
}

func (s *Store) AgentReleasePath(architecture string) (string, error) {
	if architecture != "amd64" && architecture != "arm64" {
		return "", errors.New("unsupported agent architecture")
	}
	path := filepath.Join(s.agentReleaseDir, "minerdash-agent-"+architecture)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	return path, nil
}

func (s *Store) EnrollmentToken() string {
	return s.enrollmentToken
}

func (s *Store) SetTLSFingerprint(value string) {
	s.tlsFingerprint = value
}

func (s *Store) TLSFingerprint() string {
	return s.tlsFingerprint
}

func newState() state {
	now := time.Now().UTC()
	result := state{
		Rigs:              make(map[string]*storedRig),
		Farms:             make(map[string]protocol.Farm),
		Wallets:           make(map[string]protocol.Wallet),
		Pools:             make(map[string]protocol.Pool),
		Miners:            make(map[string]protocol.MinerDefinition),
		FlightSheets:      make(map[string]protocol.FlightSheet),
		OverclockProfiles: make(map[string]protocol.OverclockProfile),
		History:           make(map[string][]protocol.Metrics),
		HistoryRecordedAt: make(map[string]time.Time),
		DriverEfficiency:  make(map[string]*protocol.DriverEfficiencyRecord),
		Schedules:         make(map[string]protocol.Schedule),
		Users:             make(map[string]*storedUser),
		EnrollmentGrants:  make(map[string]enrollmentGrant),
	}
	result.Farms["starter-gpu-farm"] = protocol.Farm{
		ID:          "starter-gpu-farm",
		Name:        "GPU Farm",
		Description: "Starter group for GPU mining rigs",
		CreatedAt:   now,
	}
	result.Farms["starter-cpu-farm"] = protocol.Farm{
		ID:          "starter-cpu-farm",
		Name:        "CPU Farm",
		Description: "Starter group for CPU mining rigs",
		CreatedAt:   now,
	}
	return result
}

func (s *Store) initializeMaps() {
	if s.state.Farms == nil {
		s.state.Farms = make(map[string]protocol.Farm)
	}
	if s.state.Wallets == nil {
		s.state.Wallets = make(map[string]protocol.Wallet)
	}
	if s.state.Pools == nil {
		s.state.Pools = make(map[string]protocol.Pool)
	}
	if s.state.Miners == nil {
		s.state.Miners = make(map[string]protocol.MinerDefinition)
	}
	if s.state.FlightSheets == nil {
		s.state.FlightSheets = make(map[string]protocol.FlightSheet)
	}
	if s.state.OverclockProfiles == nil {
		s.state.OverclockProfiles = make(map[string]protocol.OverclockProfile)
	}
	if s.state.History == nil {
		s.state.History = make(map[string][]protocol.Metrics)
	}
	if s.state.HistoryRecordedAt == nil {
		s.state.HistoryRecordedAt = make(map[string]time.Time)
	}
	if s.state.DriverEfficiency == nil {
		s.state.DriverEfficiency = make(map[string]*protocol.DriverEfficiencyRecord)
	}
	if s.state.Schedules == nil {
		s.state.Schedules = make(map[string]protocol.Schedule)
	}
	if s.state.Users == nil {
		s.state.Users = make(map[string]*storedUser)
	}
	if s.state.EnrollmentGrants == nil {
		s.state.EnrollmentGrants = make(map[string]enrollmentGrant)
	}
}

func (s *Store) IsAdmin(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(s.adminHash)) == 1
}

func (s *Store) SetRecoveryCode(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recoveryCode = value
	s.recoveryHash = hashToken(value)
}

func (s *Store) RecoveryCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recoveryCode
}

func (s *Store) Enroll(name, enrollmentToken string) (protocol.EnrollResponse, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return protocol.EnrollResponse{}, errors.New("rig name is required")
	}
	id, err := randomToken(16)
	if err != nil {
		return protocol.EnrollResponse{}, err
	}
	token, err := randomToken(32)
	if err != nil {
		return protocol.EnrollResponse{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenHash := hashToken(enrollmentToken)
	sharedToken := enrollmentToken != "" && subtle.ConstantTimeCompare([]byte(tokenHash), []byte(s.enrollmentHash)) == 1
	grant, oneTimeToken := s.state.EnrollmentGrants[tokenHash]
	if oneTimeToken && (now.After(grant.ExpiresAt) || !strings.EqualFold(grant.RigName, name)) {
		oneTimeToken = false
	}
	if !sharedToken && !oneTimeToken {
		return protocol.EnrollResponse{}, ErrUnauthorized
	}
	for _, rig := range s.state.Rigs {
		if strings.EqualFold(rig.Name, name) {
			return protocol.EnrollResponse{}, errors.New("rig name is already in use; choose a unique name")
		}
	}
	if oneTimeToken {
		delete(s.state.EnrollmentGrants, tokenHash)
	}
	s.state.Rigs[id] = &storedRig{
		Rig:       protocol.Rig{ID: id, Name: name, EnrolledAt: now},
		TokenHash: hashToken(token),
	}
	s.recordLocked("enroll", "worker", id, map[string]any{"name": name})
	if err := s.saveLocked(); err != nil {
		delete(s.state.Rigs, id)
		return protocol.EnrollResponse{}, err
	}
	return protocol.EnrollResponse{AgentID: id, Token: token}, nil
}

func (s *Store) CreateEnrollmentGrant(name string, lifetime time.Duration) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("rig name is required")
	}
	if lifetime <= 0 || lifetime > 30*24*time.Hour {
		return "", errors.New("enrollment grant lifetime must be between one second and 30 days")
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, grant := range s.state.EnrollmentGrants {
		if !grant.ExpiresAt.After(now) {
			delete(s.state.EnrollmentGrants, hash)
		}
	}
	for _, rig := range s.state.Rigs {
		if strings.EqualFold(rig.Name, name) {
			return "", errors.New("rig name is already in use; choose a unique name")
		}
	}
	for _, grant := range s.state.EnrollmentGrants {
		if grant.ExpiresAt.After(now) && strings.EqualFold(grant.RigName, name) {
			return "", errors.New("a pending USB pairing already uses this rig name")
		}
	}
	s.state.EnrollmentGrants[hashToken(token)] = enrollmentGrant{
		RigName:   name,
		ExpiresAt: now.Add(lifetime),
	}
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) RevokeEnrollmentGrant(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state.EnrollmentGrants, hashToken(token))
	return s.saveLocked()
}

func (s *Store) AuthenticateAgent(id, token string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rig, ok := s.state.Rigs[id]
	if !ok || token == "" || subtle.ConstantTimeCompare([]byte(rig.TokenHash), []byte(hashToken(token))) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (s *Store) Heartbeat(id string, metrics protocol.Metrics, ipAddress string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rig, ok := s.state.Rigs[id]
	if !ok {
		return ErrNotFound
	}
	rig.LastHeartbeat = time.Now().UTC()
	rig.IPAddress = ipAddress
	metrics.MiningCoin, metrics.MiningAlgorithm = s.gpuWorkloadLocked(rig.Desired.FlightSheetID)
	rig.Metrics = metrics
	if err := s.recordMetricsLocked(id, metrics); err != nil {
		return err
	}
	s.evaluateAlertsLocked(rig)
	return s.saveLocked()
}

func (s *Store) ListRigs(now time.Time) []protocol.Rig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.Rig, 0, len(s.state.Rigs))
	for _, stored := range s.state.Rigs {
		rig := stored.Rig
		rig.Online = !rig.LastHeartbeat.IsZero() && now.Sub(rig.LastHeartbeat) < 30*time.Second
		rig.DriverEfficiency = s.driverEfficiencySummariesLocked(&rig)
		rig.OverclockPresets = s.overclockKnowledgePresetsLocked(&rig)
		result = append(result, rig)
	}
	return result
}

func (s *Store) AddCommand(rigID, action, profile string) (protocol.Command, error) {
	return s.AddCommandRequest(rigID, protocol.CommandRequest{Action: action, Profile: profile})
}

func (s *Store) AddCommandRequest(rigID string, request protocol.CommandRequest) (protocol.Command, error) {
	if !validCommandAction(request.Action) {
		return protocol.Command{}, errors.New("unsupported command action")
	}
	if request.Action == "smart-tune" {
		if request.SmartTune == nil {
			return protocol.Command{}, errors.New("smart tune settings are required")
		}
		if request.SmartTune.Mode != "balanced" || request.SmartTune.MaxPowerW <= 0 || request.SmartTune.MaxPowerW > 115 {
			return protocol.Command{}, errors.New("smart tune requires balanced mode and a power limit from 1 to 115 W")
		}
		if request.SmartTune.MaxTemperatureC < 60 || request.SmartTune.MaxTemperatureC > 85 {
			return protocol.Command{}, errors.New("smart tune temperature limit must be from 60 to 85 C")
		}
	}
	if request.Action == "gpu-driver-update" {
		if request.DriverUpdate == nil {
			return protocol.Command{}, errors.New("GPU driver update settings are required")
		}
		request.DriverUpdate.Vendor = strings.ToUpper(strings.TrimSpace(request.DriverUpdate.Vendor))
		if request.DriverUpdate.Channel != "stable" {
			return protocol.Command{}, errors.New("only the stable distribution driver channel is supported")
		}
		if request.DriverUpdate.Vendor != "NVIDIA" && request.DriverUpdate.Vendor != "AMD" {
			return protocol.Command{}, errors.New("GPU driver vendor must be NVIDIA or AMD")
		}
	}
	id, err := randomToken(12)
	if err != nil {
		return protocol.Command{}, err
	}
	command := protocol.Command{
		ID: id, Action: request.Action, Profile: request.Profile, SmartTune: request.SmartTune, DriverUpdate: request.DriverUpdate,
		CreatedAt: time.Now().UTC(), Status: "pending",
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return protocol.Command{}, ErrNotFound
	}
	if request.Action == "smart-tune" {
		if len(rig.Metrics.GPUs) == 0 || !rig.Metrics.Miner.Running {
			return protocol.Command{}, errors.New("smart tune requires an online GPU rig with a running miner")
		}
		if !rig.Metrics.SmartTuneSupported {
			return protocol.Command{}, errors.New("smart tune requires the latest rig agent")
		}
		if rig.Desired.OverclockProfileID == "" {
			return protocol.Command{}, errors.New("smart tune requires an assigned overclock profile")
		}
		profile, ok := s.state.OverclockProfiles[rig.Desired.OverclockProfileID]
		if !ok || profile.Vendor != "NVIDIA" {
			return protocol.Command{}, errors.New("smart tune requires an assigned NVIDIA overclock profile")
		}
		for otherRigID, otherRig := range s.state.Rigs {
			if otherRigID != rigID && otherRig.Desired.OverclockProfileID == profile.ID {
				return protocol.Command{}, errors.New("smart tune requires an overclock profile used only by this rig")
			}
		}
		command.Profile = profile.ID
		for _, existing := range rig.Commands {
			if existing.Action == "smart-tune" && (existing.Status == "pending" || existing.Status == "running") {
				return protocol.Command{}, errors.New("smart tune is already running on this rig")
			}
		}
		for _, summary := range s.driverEfficiencySummariesLocked(&rig.Rig) {
			if strings.EqualFold(summary.Vendor, request.DriverUpdate.Vendor) &&
				summary.Status == "current-best" && summary.ComparedDrivers > 1 {
				return protocol.Command{}, fmt.Errorf(
					"performance guard blocked the update: driver %s is the best locally tested %s driver for %s on %s",
					summary.CurrentDriver, summary.Vendor, summary.Coin, summary.Model,
				)
			}
		}
	}
	if request.Action == "gpu-driver-update" {
		if rig.LastHeartbeat.IsZero() || time.Since(rig.LastHeartbeat) >= 30*time.Second {
			return protocol.Command{}, errors.New("GPU driver update requires an online rig")
		}
		if !rig.Metrics.DriverUpdateSupported {
			return protocol.Command{}, errors.New("GPU driver update requires the latest Miner Dash agent")
		}
		foundVendor := false
		for _, gpu := range rig.Metrics.GPUs {
			if strings.EqualFold(gpu.Vendor, request.DriverUpdate.Vendor) {
				foundVendor = true
				break
			}
		}
		if !foundVendor {
			return protocol.Command{}, fmt.Errorf("this rig has no %s GPUs", request.DriverUpdate.Vendor)
		}
		for _, existing := range rig.Commands {
			if existing.Action == "gpu-driver-update" && (existing.Status == "pending" || existing.Status == "running") {
				return protocol.Command{}, errors.New("a GPU driver update is already queued or running on this rig")
			}
		}
	}
	rig.Commands = append(rig.Commands, command)
	s.recordLocked("queue", "command", command.ID, map[string]any{"rig_id": rigID, "action": request.Action})
	if err := s.saveLocked(); err != nil {
		rig.Commands = rig.Commands[:len(rig.Commands)-1]
		return protocol.Command{}, err
	}
	return command, nil
}

func (s *Store) AddCommands(rigIDs []string, action, profile string) ([]protocol.Command, error) {
	if !validCommandAction(action) {
		return nil, errors.New("unsupported command action")
	}
	if action == "smart-tune" {
		return nil, errors.New("smart tune must be started on one rig at a time")
	}
	rigIDs = uniqueStrings(rigIDs)
	if len(rigIDs) == 0 {
		return nil, errors.New("at least one worker is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rigID := range rigIDs {
		if _, ok := s.state.Rigs[rigID]; !ok {
			return nil, fmt.Errorf("worker %s does not exist", rigID)
		}
	}
	commands := make([]protocol.Command, 0, len(rigIDs))
	for _, rigID := range rigIDs {
		command := protocol.Command{
			ID: mustToken(12), Action: action, Profile: profile,
			CreatedAt: time.Now().UTC(), Status: "pending",
		}
		s.state.Rigs[rigID].Commands = append(s.state.Rigs[rigID].Commands, command)
		commands = append(commands, command)
	}
	s.recordLocked("queue", "bulk-command", "", map[string]any{"action": action, "workers": len(rigIDs)})
	return commands, s.saveLocked()
}

func validCommandAction(action string) bool {
	switch action {
	case "start", "stop", "restart", "reboot", "shutdown", "logs", "smart-tune", "stop-smart-tune", "gpu-driver-update":
		return true
	default:
		return false
	}
}

func (s *Store) ListCommands(rigID string, limit int) ([]protocol.Command, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	start := len(rig.Commands) - limit
	if start < 0 {
		start = 0
	}
	result := append([]protocol.Command(nil), rig.Commands[start:]...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, nil
}

func (s *Store) NextCommand(rigID string) (protocol.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return protocol.Command{}, ErrNotFound
	}
	now := time.Now().UTC()
	for i := range rig.Commands {
		if rig.Commands[i].Status == "pending" || (rig.Commands[i].Status == "running" && rig.Commands[i].LeaseUntil.Before(now)) {
			rig.Commands[i].Status = "running"
			rig.Commands[i].Attempt++
			lease := 30 * time.Second
			if rig.Commands[i].Action == "smart-tune" {
				lease = time.Hour
			} else if rig.Commands[i].Action == "gpu-driver-update" {
				lease = 30 * time.Minute
			}
			rig.Commands[i].LeaseUntil = now.Add(lease)
			if err := s.saveLocked(); err != nil {
				rig.Commands[i].Status = "pending"
				rig.Commands[i].LeaseUntil = time.Time{}
				return protocol.Command{}, err
			}
			return rig.Commands[i], nil
		}
	}
	return protocol.Command{}, ErrNotFound
}

func (s *Store) FinishCommand(rigID, commandID, commandError, output string) error {
	return s.FinishCommandResult(rigID, commandID, protocol.CommandResult{Error: commandError, Output: output})
}

func (s *Store) FinishCommandResult(rigID, commandID string, result protocol.CommandResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return ErrNotFound
	}
	for i := range rig.Commands {
		if rig.Commands[i].ID == commandID {
			if rig.Commands[i].Status == "complete" || rig.Commands[i].Status == "failed" {
				return nil
			}
			if rig.Commands[i].Status != "running" {
				return errors.New("command is not running")
			}
			rig.Commands[i].Status = "complete"
			if result.Error == "" && rig.Commands[i].Action == "smart-tune" {
				profileID := rig.Commands[i].Profile
				profile, ok := s.state.OverclockProfiles[profileID]
				if !ok || rig.Desired.OverclockProfileID != profileID {
					result.Error = "assigned overclock profile changed during smart tune"
				} else if s.overclockProfileIsSharedLocked(profileID, rigID) {
					result.Error = "assigned overclock profile became shared during smart tune"
				} else if err := applySmartTuneSettings(&profile, result.TunedSettings); err != nil {
					result.Error = err.Error()
				} else {
					s.state.OverclockProfiles[profile.ID] = profile
					rig.Desired.Revision++
				}
			}
			if result.Error != "" {
				rig.Commands[i].Status = "failed"
				rig.Commands[i].Error = result.Error
			}
			rig.Commands[i].Output = result.Output
			s.recordLocked(rig.Commands[i].Status, "command", commandID, map[string]any{"rig_id": rigID, "action": rig.Commands[i].Action})
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

func (s *Store) overclockProfileIsSharedLocked(profileID, rigID string) bool {
	for otherRigID, otherRig := range s.state.Rigs {
		if otherRigID != rigID && otherRig.Desired.OverclockProfileID == profileID {
			return true
		}
	}
	return false
}

func applySmartTuneSettings(profile *protocol.OverclockProfile, tuned []protocol.GPUOverclock) error {
	if profile.Vendor != "NVIDIA" || len(tuned) == 0 {
		return errors.New("smart tune returned no NVIDIA settings")
	}
	bySelector := make(map[string]int, len(tuned))
	for _, setting := range tuned {
		if setting.Selector == "" || setting.PowerLimitW <= 0 || setting.PowerLimitW > 115 {
			return errors.New("smart tune returned an invalid or unsafe power limit")
		}
		bySelector[setting.Selector] = setting.PowerLimitW
	}
	for index := range profile.Settings {
		if profile.Settings[index].Selector == "" || profile.Settings[index].Selector == "*" {
			profile.Settings[index].PowerLimitW = 0
			continue
		}
		if power, ok := bySelector[profile.Settings[index].Selector]; ok {
			profile.Settings[index].PowerLimitW = power
			delete(bySelector, profile.Settings[index].Selector)
		}
	}
	for selector, power := range bySelector {
		profile.Settings = append(profile.Settings, protocol.GPUOverclock{Selector: selector, PowerLimitW: power})
	}
	return nil
}

func (s *Store) DeleteRig(rigID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return ErrNotFound
	}
	delete(s.state.Rigs, rigID)
	delete(s.state.History, rigID)
	delete(s.state.HistoryRecordedAt, rigID)
	for id, schedule := range s.state.Schedules {
		filtered := schedule.RigIDs[:0]
		for _, scheduledRigID := range schedule.RigIDs {
			if scheduledRigID != rigID {
				filtered = append(filtered, scheduledRigID)
			}
		}
		schedule.RigIDs = filtered
		if len(filtered) == 0 {
			schedule.Enabled = false
		}
		s.state.Schedules[id] = schedule
	}
	s.recordLocked("delete", "worker", rigID, map[string]any{"name": rig.Name})
	if err := s.saveLocked(); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.historyDir, rigID)); err != nil {
		return fmt.Errorf("remove worker telemetry: %w", err)
	}
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return s.rollbackLocked(err)
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return s.rollbackLocked(err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return s.rollbackLocked(err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return s.rollbackLocked(err)
	}
	s.lastSaved = append(s.lastSaved[:0], data...)
	return nil
}

func (s *Store) rollbackLocked(cause error) error {
	if len(s.lastSaved) == 0 {
		return cause
	}
	var restored state
	if err := json.Unmarshal(s.lastSaved, &restored); err != nil {
		return fmt.Errorf("%v; restore in-memory state: %w", cause, err)
	}
	s.state = restored
	s.initializeMaps()
	return cause
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
