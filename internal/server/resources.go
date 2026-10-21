package server

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

func (s *Store) ListFarms() []protocol.Farm {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.state.Farms, func(v protocol.Farm) string { return v.Name })
}

func (s *Store) SaveFarm(value protocol.Farm) (protocol.Farm, error) {
	if strings.TrimSpace(value.Name) == "" {
		return value, errors.New("farm name is required")
	}
	if math.IsNaN(value.ElectricityRateUSDPerKWh) || math.IsInf(value.ElectricityRateUSDPerKWh, 0) || value.ElectricityRateUSDPerKWh < 0 || value.ElectricityRateUSDPerKWh > 100 {
		return value, errors.New("electricity rate must be between 0 and 100 USD per kWh")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if value.ID == "" {
		value.ID = mustToken(12)
		value.CreatedAt = now
	} else if previous, ok := s.state.Farms[value.ID]; ok {
		value.CreatedAt = previous.CreatedAt
	} else {
		return value, ErrNotFound
	}
	value.Name = strings.TrimSpace(value.Name)
	s.state.Farms[value.ID] = value
	s.recordLocked("save", "farm", value.ID, map[string]any{"name": value.Name})
	return value, s.saveLocked()
}

func (s *Store) ListWallets() []protocol.Wallet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.state.Wallets, func(v protocol.Wallet) string { return v.Name })
}

func (s *Store) SaveWallet(value protocol.Wallet) (protocol.Wallet, error) {
	if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Coin) == "" || strings.TrimSpace(value.Address) == "" {
		return value, errors.New("wallet name, coin, and address are required")
	}
	if strings.ContainsAny(value.Address, " \t\r\n") {
		return value, errors.New("wallet address cannot contain whitespace")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value.ID == "" {
		value.ID = mustToken(12)
		value.CreatedAt = time.Now().UTC()
	} else if previous, ok := s.state.Wallets[value.ID]; ok {
		value.CreatedAt = previous.CreatedAt
	} else {
		return value, ErrNotFound
	}
	value.Coin = strings.ToUpper(strings.TrimSpace(value.Coin))
	value.Name = strings.TrimSpace(value.Name)
	s.state.Wallets[value.ID] = value
	s.bumpWorkersForResourceLocked("wallet", value.ID)
	s.recordLocked("save", "wallet", value.ID, map[string]any{"name": value.Name, "coin": value.Coin})
	return value, s.saveLocked()
}

func (s *Store) ListPools() []protocol.Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.state.Pools, func(v protocol.Pool) string { return v.Name })
}

func (s *Store) SavePool(value protocol.Pool) (protocol.Pool, error) {
	if strings.TrimSpace(value.Name) == "" {
		return value, errors.New("pool name is required")
	}
	value.URL = strings.TrimSpace(value.URL)
	value.DashboardURL = strings.TrimSpace(value.DashboardURL)
	value.StatsURL = strings.TrimRight(strings.TrimSpace(value.StatsURL), "/")
	value.StatsPoolID = strings.TrimSpace(value.StatsPoolID)
	if !validMiningEndpoint(value.URL) {
		return value, errors.New("pool URL must use stratum+tcp, stratum+ssl, stratum+tls, stratum+tcps, http, https, ws, or wss")
	}
	if value.DashboardURL != "" && !validHTTPURLTemplate(value.DashboardURL) {
		return value, errors.New("pool worker page URL must be an HTTP or HTTPS URL; supported placeholders are {WALLET}, {WORKER}, and {COIN}")
	}
	if (value.StatsURL == "") != (value.StatsPoolID == "") {
		return value, errors.New("pool statistics URL and pool ID must be configured together")
	}
	if value.StatsURL != "" && !validHTTPURL(value.StatsURL) {
		return value, errors.New("pool statistics URL must be an HTTP or HTTPS URL")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	affectsMining := false
	if value.ID == "" {
		value.ID = mustToken(12)
		value.CreatedAt = time.Now().UTC()
	} else if previous, ok := s.state.Pools[value.ID]; ok {
		value.CreatedAt = previous.CreatedAt
		affectsMining = previous.URL != value.URL || previous.Password != value.Password
	} else {
		return value, ErrNotFound
	}
	value.Name = strings.TrimSpace(value.Name)
	s.state.Pools[value.ID] = value
	if affectsMining {
		s.bumpWorkersForResourceLocked("pool", value.ID)
	}
	s.recordLocked("save", "pool", value.ID, map[string]any{"name": value.Name, "url": value.URL})
	return value, s.saveLocked()
}

func (s *Store) ListMiners() []protocol.MinerDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.state.Miners, func(v protocol.MinerDefinition) string { return v.Name + v.Version })
}

func (s *Store) SaveMiner(value protocol.MinerDefinition) (protocol.MinerDefinition, error) {
	if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Algorithm) == "" || (!value.ManagedBinary && strings.TrimSpace(value.Profile) == "") {
		return value, errors.New("miner name, algorithm, and either a local profile or managed binary are required")
	}
	if value.ManagedBinary && (value.BinaryName == "" || filepath.Base(value.BinaryName) != value.BinaryName) {
		if value.PackageFormat != "tar.gz" || !validBundlePath(value.EntryPoint) {
			return value, errors.New("managed miner binary_name must be a file name without directories")
		}
	}
	if value.PackageFormat != "" && value.PackageFormat != "binary" && value.PackageFormat != "tar.gz" {
		return value, errors.New("miner package_format must be binary or tar.gz")
	}
	if value.PackageFormat == "tar.gz" && !validBundlePath(value.EntryPoint) {
		return value, errors.New("bundle entry_point must be a safe relative path")
	}
	for name := range value.Environment {
		if !validEnvironmentName(name) {
			return value, fmt.Errorf("invalid environment variable name %q", name)
		}
	}
	if value.StatsType != "" && value.StatsType != "xmrig" && value.StatsType != "miniz" && value.StatsType != "srbminer" && value.StatsType != "trex" && value.StatsType != "wildrig" {
		return value, errors.New("stats_type must be empty, xmrig, miniz, srbminer, trex, or wildrig")
	}
	if value.StatsType != "" {
		parsed, err := url.Parse(value.StatsURL)
		if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") {
			return value, errors.New("custom miner statistics URL must use local HTTP")
		}
	}
	if value.BinarySHA256 != "" && (len(value.BinarySHA256) != 64 || !isHex(value.BinarySHA256)) {
		return value, errors.New("binary_sha256 must be a 64-character hexadecimal digest")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if value.CatalogID != "" {
		for _, existing := range s.state.Miners {
			if existing.CatalogID != value.CatalogID || existing.ID == value.ID {
				continue
			}
			if value.ID == "" {
				value.ID = existing.ID
				break
			}
			return value, errors.New("catalog miner is already installed")
		}
	}
	if value.ID == "" {
		value.ID = mustToken(12)
		value.CreatedAt = time.Now().UTC()
	} else if previous, ok := s.state.Miners[value.ID]; ok {
		value.CreatedAt = previous.CreatedAt
		if value.CatalogID == "" {
			value.CatalogID = previous.CatalogID
		}
		if value.ManagedBinary && value.BinarySHA256 == "" && value.BinaryName == previous.BinaryName {
			value.BinarySHA256 = previous.BinarySHA256
		}
		if value.ManagedBinary && value.PackageSHA256 == "" && value.EntryPoint == previous.EntryPoint {
			value.PackageSHA256 = previous.PackageSHA256
		}
	} else {
		return value, ErrNotFound
	}
	value.Name = strings.TrimSpace(value.Name)
	value.Profile = strings.TrimSpace(value.Profile)
	s.state.Miners[value.ID] = value
	s.bumpWorkersForResourceLocked("miner", value.ID)
	s.recordLocked("save", "miner", value.ID, map[string]any{"name": value.Name, "version": value.Version})
	return value, s.saveLocked()
}

func validEnvironmentName(value string) bool {
	if value == "" || (value[0] != '_' && (value[0] < 'A' || value[0] > 'Z') && (value[0] < 'a' || value[0] > 'z')) {
		return false
	}
	for _, character := range value[1:] {
		if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func (s *Store) ListFlightSheets() []protocol.FlightSheet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.state.FlightSheets, func(v protocol.FlightSheet) string { return v.Name })
}

func (s *Store) SaveFlightSheet(value protocol.FlightSheet) (protocol.FlightSheet, error) {
	if strings.TrimSpace(value.Name) == "" {
		return value, errors.New("flight sheet name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	wallet, walletOK := s.state.Wallets[value.WalletID]
	_, poolOK := s.state.Pools[value.PoolID]
	miner, minerOK := s.state.Miners[value.MinerID]
	if !walletOK || !poolOK || !minerOK {
		return value, errors.New("wallet, pool, and miner must exist")
	}
	if err := validateFlightSheetMinerReady(miner); err != nil {
		return value, err
	}
	if err := validateCatalogMinerCoin(miner.CatalogID, value.Coin); err != nil {
		return value, err
	}
	value.Algorithm = catalogAlgorithmForCoin(miner.CatalogID, value.Coin, value.Algorithm)
	if miner.CatalogID == "trex" && strings.EqualFold(strings.TrimSpace(value.Coin), "VEIL") &&
		!strings.EqualFold(strings.TrimSpace(value.Algorithm), "progpow-veil") {
		return value, errors.New("T-Rex mining VEIL requires algorithm progpow-veil; generic progpow uses an incompatible pool login protocol")
	}
	if err := validateBundledMinerAlgorithm(miner.CatalogID, value.Algorithm); err != nil {
		return value, err
	}
	if entry, ok := catalogEntry(miner.CatalogID); ok && entry.Algorithm != "" && value.Algorithm != "" && !strings.EqualFold(value.Algorithm, entry.Algorithm) {
		return value, fmt.Errorf("%s supports only %s", entry.Name, entry.Algorithm)
	}
	if value.FailoverAfterSeconds < 0 || value.FailoverAfterSeconds > 600 ||
		value.ReturnAfterSeconds < 0 || value.ReturnAfterSeconds > 600 {
		return value, errors.New("failover and return delays must be between 0 and 600 seconds")
	}
	if value.FallbackFlightSheetID != "" {
		fallback, ok := s.state.FlightSheets[value.FallbackFlightSheetID]
		if !ok {
			return value, errors.New("fallback flight sheet does not exist")
		}
		if value.ID != "" && fallback.ID == value.ID {
			return value, errors.New("a flight sheet cannot fall back to itself")
		}
		if miner.CatalogID != "epic-miner" {
			return value, errors.New("automatic algorithm failover currently requires Epic CPU Miner as the primary miner")
		}
		if !strings.EqualFold(value.DeviceType, fallback.DeviceType) {
			return value, errors.New("primary and fallback flight sheets must use the same device type")
		}
		if fallback.FallbackFlightSheetID != "" {
			return value, errors.New("fallback flight sheets cannot contain another fallback")
		}
	}
	hasSecondary := value.SecondaryDeviceType != "" || value.SecondaryCoin != "" || value.SecondaryAlgorithm != "" || value.SecondaryWalletID != "" || value.SecondaryPoolID != ""
	if hasSecondary {
		secondaryWallet, secondaryWalletOK := s.state.Wallets[value.SecondaryWalletID]
		_, secondaryPoolOK := s.state.Pools[value.SecondaryPoolID]
		miner := s.state.Miners[value.MinerID]
		if !secondaryWalletOK || !secondaryPoolOK || value.SecondaryCoin == "" || value.SecondaryAlgorithm == "" {
			return value, errors.New("secondary coin, wallet, pool, and algorithm must all be configured")
		}
		if value.SecondaryDeviceType != "CPU" && value.SecondaryDeviceType != "GPU" {
			return value, errors.New("secondary device type must be CPU or GPU")
		}
		if value.SecondaryDeviceType == value.DeviceType {
			return value, errors.New("primary and secondary workloads must use different device types")
		}
		if !strings.EqualFold(value.SecondaryCoin, secondaryWallet.Coin) {
			return value, errors.New("secondary flight sheet coin must match its wallet coin")
		}
		if miner.CatalogID != "srbminer-multi" {
			return value, errors.New("CPU and GPU dual workloads currently require SRBMiner-MULTI")
		}
	}
	if value.Coin == "" {
		value.Coin = wallet.Coin
	}
	if !strings.EqualFold(value.Coin, wallet.Coin) {
		return value, errors.New("flight sheet coin must match wallet coin")
	}
	if value.ID == "" {
		value.ID = mustToken(12)
		value.CreatedAt = time.Now().UTC()
	} else if previous, ok := s.state.FlightSheets[value.ID]; ok {
		value.CreatedAt = previous.CreatedAt
	} else {
		return value, ErrNotFound
	}
	value.Name = strings.TrimSpace(value.Name)
	value.Coin = strings.ToUpper(strings.TrimSpace(value.Coin))
	value.SecondaryCoin = strings.ToUpper(strings.TrimSpace(value.SecondaryCoin))
	value.PoolURLOverride = strings.TrimSpace(value.PoolURLOverride)
	value.WorkerName = strings.TrimSpace(value.WorkerName)
	value.WalletTemplate = strings.TrimSpace(value.WalletTemplate)
	if value.PoolURLOverride != "" && !validMiningEndpoint(value.PoolURLOverride) {
		return value, errors.New("flight sheet pool override must use stratum+tcp, stratum+ssl, stratum+tls, stratum+tcps, http, https, ws, or wss")
	}
	for index, endpoint := range value.BackupPoolURLs {
		value.BackupPoolURLs[index] = strings.TrimSpace(endpoint)
		if !validMiningEndpoint(value.BackupPoolURLs[index]) {
			return value, errors.New("flight sheet backup pool URLs must use a supported mining endpoint scheme")
		}
	}
	value.ExtraArguments = compactStrings(value.ExtraArguments)
	s.state.FlightSheets[value.ID] = value
	s.bumpWorkersForResourceLocked("flight-sheet", value.ID)
	s.recordLocked("save", "flight-sheet", value.ID, map[string]any{"name": value.Name, "coin": value.Coin})
	return value, s.saveLocked()
}

func (s *Store) ListOverclockProfiles() []protocol.OverclockProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.state.OverclockProfiles, func(v protocol.OverclockProfile) string { return v.Name })
}

func (s *Store) SaveOverclockProfile(value protocol.OverclockProfile) (protocol.OverclockProfile, error) {
	value.Vendor = strings.ToUpper(strings.TrimSpace(value.Vendor))
	if strings.TrimSpace(value.Name) == "" || (value.Vendor != "NVIDIA" && value.Vendor != "AMD") {
		return value, errors.New("profile name and vendor NVIDIA or AMD are required")
	}
	for _, setting := range value.Settings {
		if setting.FanPercent < 0 || setting.FanPercent > 100 || setting.PowerLimitW < 0 {
			return value, errors.New("fan percent must be 0-100 and power limit cannot be negative")
		}
		if setting.CoreClockMHz > 0 && setting.CoreOffsetMHz != 0 {
			return value, errors.New("locked core clock and core offset cannot be used together")
		}
		if setting.MemoryClockMHz > 0 && setting.MemoryOffsetMHz != 0 {
			return value, errors.New("locked memory clock and memory offset cannot be used together")
		}
		if setting.Selector != "*" {
			index, err := strconv.Atoi(setting.Selector)
			if err != nil || index < 0 {
				return value, errors.New("GPU selector must be * or a non-negative index")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value.ID == "" {
		value.ID = mustToken(12)
		value.CreatedAt = time.Now().UTC()
	} else if previous, ok := s.state.OverclockProfiles[value.ID]; ok {
		value.CreatedAt = previous.CreatedAt
	} else {
		return value, ErrNotFound
	}
	value.Name = strings.TrimSpace(value.Name)
	s.state.OverclockProfiles[value.ID] = value
	s.bumpWorkersForResourceLocked("overclock-profile", value.ID)
	s.recordLocked("save", "overclock-profile", value.ID, map[string]any{"name": value.Name, "vendor": value.Vendor})
	return value, s.saveLocked()
}

func (s *Store) AssignWorker(rigID string, assignment protocol.WorkerAssignment) (protocol.Rig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return protocol.Rig{}, ErrNotFound
	}
	if assignment.FarmID != "" {
		if _, ok := s.state.Farms[assignment.FarmID]; !ok {
			return protocol.Rig{}, errors.New("farm does not exist")
		}
	}
	if assignment.FlightSheetID != "" {
		if _, ok := s.state.FlightSheets[assignment.FlightSheetID]; !ok {
			return protocol.Rig{}, errors.New("flight sheet does not exist")
		}
	}
	if assignment.OverclockProfileID != "" {
		if _, ok := s.state.OverclockProfiles[assignment.OverclockProfileID]; !ok {
			return protocol.Rig{}, errors.New("overclock profile does not exist")
		}
	}
	if !assignment.Watchdog.Enabled {
		assignment.Watchdog = protocol.WatchdogPolicy{}
	}
	if !assignment.Autofan.Enabled {
		assignment.Autofan = protocol.AutofanPolicy{}
	}
	if assignment.Autofan.Enabled && (assignment.Autofan.TargetTemperatureC <= 0 || assignment.Autofan.MinimumFanPercent < 0 ||
		assignment.Autofan.MaximumFanPercent > 100 || assignment.Autofan.MinimumFanPercent > assignment.Autofan.MaximumFanPercent) {
		return protocol.Rig{}, errors.New("autofan requires a target temperature and a valid 0-100 fan range")
	}
	if assignment.Watchdog.Enabled && (assignment.Watchdog.MaxTemperatureC < 0 || assignment.Watchdog.MaxTemperatureC > 110 ||
		assignment.Watchdog.MinHashrate < 0 || (assignment.Watchdog.MinHashrate > 0 && assignment.Watchdog.RestartAfterSeconds < 10) ||
		assignment.Watchdog.RebootAfterFailures < 0) {
		return protocol.Rig{}, errors.New("watchdog limits are invalid")
	}
	if assignment.EstimatedPowerW < 0 || assignment.EstimatedPowerW > 100000 ||
		assignment.PowerOffsetW < 0 || assignment.PowerOffsetW > 100000 {
		return protocol.Rig{}, errors.New("power estimates must be between 0 and 100000 watts")
	}
	if strings.TrimSpace(assignment.Name) != "" {
		name := strings.TrimSpace(assignment.Name)
		for id, existing := range s.state.Rigs {
			if id != rigID && strings.EqualFold(existing.Name, name) {
				return protocol.Rig{}, errors.New("worker name is already in use")
			}
		}
		rig.Name = name
	}
	rig.FarmID = assignment.FarmID
	rig.Tags = uniqueStrings(assignment.Tags)
	currentWatchdog := rig.Desired.Watchdog
	if !currentWatchdog.Enabled {
		currentWatchdog = protocol.WatchdogPolicy{}
	}
	currentAutofan := rig.Desired.Autofan
	if !currentAutofan.Enabled {
		currentAutofan = protocol.AutofanPolicy{}
	}
	desiredChanged := rig.Desired.FlightSheetID != assignment.FlightSheetID ||
		rig.Desired.OverclockProfileID != assignment.OverclockProfileID ||
		currentWatchdog != assignment.Watchdog ||
		currentAutofan != assignment.Autofan
	if desiredChanged {
		rig.Desired.Revision++
	}
	rig.Desired.FlightSheetID = assignment.FlightSheetID
	rig.Desired.OverclockProfileID = assignment.OverclockProfileID
	rig.Desired.EstimatedPowerW = assignment.EstimatedPowerW
	rig.Desired.PowerOffsetW = assignment.PowerOffsetW
	rig.Desired.Watchdog = assignment.Watchdog
	rig.Desired.Autofan = assignment.Autofan
	s.recordLocked("assign", "worker", rigID, map[string]any{"revision": rig.Desired.Revision})
	return rig.Rig, s.saveLocked()
}

func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func validHTTPURLTemplate(raw string) bool {
	sample := strings.NewReplacer(
		"{WALLET}", "wallet",
		"{WORKER}", "worker",
		"{COIN}", "coin",
	).Replace(raw)
	return validHTTPURL(sample)
}

func (s *Store) ResolvedWorkerConfiguration(rigID string) (protocol.ResolvedConfiguration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return protocol.ResolvedConfiguration{}, ErrNotFound
	}
	result := protocol.ResolvedConfiguration{
		Revision: rig.Desired.Revision, GPUPowerLimitW: s.state.GPUPowerPolicy.MaxPowerW,
		Watchdog: rig.Desired.Watchdog, Autofan: rig.Desired.Autofan,
	}
	if rig.Desired.FlightSheetID != "" {
		sheet, ok := s.state.FlightSheets[rig.Desired.FlightSheetID]
		if !ok {
			return result, errors.New("assigned flight sheet no longer exists")
		}
		workload := s.resolveWorkloadLocked(rig, sheet)
		if err := validateFlightSheetMinerReady(workload.Miner); err != nil {
			return result, fmt.Errorf("flight sheet %q cannot start: %w", sheet.Name, err)
		}
		if err := validateBundledMinerAlgorithm(workload.Miner.CatalogID, workload.Miner.Algorithm); err != nil {
			return result, fmt.Errorf("flight sheet %q cannot start: %w", sheet.Name, err)
		}
		result.Miner = workload.Miner
		result.Wallet = workload.Wallet
		result.Pool = workload.Pool
		result.MiningUser = workload.MiningUser
		result.WorkerName = workload.WorkerName
		if sheet.FallbackFlightSheetID != "" {
			fallback, ok := s.state.FlightSheets[sheet.FallbackFlightSheetID]
			if !ok {
				return result, errors.New("assigned fallback flight sheet no longer exists")
			}
			resolvedFallback := s.resolveWorkloadLocked(rig, fallback)
			result.Fallback = &resolvedFallback
			result.FailoverAfterSeconds = sheet.FailoverAfterSeconds
			result.ReturnAfterSeconds = sheet.ReturnAfterSeconds
		}
	}
	if rig.Desired.OverclockProfileID != "" {
		result.Overclock = s.state.OverclockProfiles[rig.Desired.OverclockProfileID]
	}
	result.Overclock = applyGPUPowerPolicyToProfile(result.Overclock, result.GPUPowerLimitW, rig.Metrics.GPUs)
	return result, nil
}

func validateFlightSheetMinerReady(miner protocol.MinerDefinition) error {
	if !miner.ManagedBinary || miner.Profile != "" || !strings.EqualFold(strings.TrimSpace(miner.Version), "package-required") {
		return nil
	}
	if entry, ok := catalogEntry(miner.CatalogID); ok && entry.AutomaticInstall {
		return nil
	}
	name := strings.TrimSpace(miner.Name)
	if name == "" {
		name = "Selected miner"
	}
	return fmt.Errorf("%s is not installed: its Linux package metadata and SHA-256 are missing. Choose an installed miner, or install/import its Linux package before applying this flight sheet", name)
}

func (s *Store) GPUPowerPolicy() protocol.GPUPowerPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.GPUPowerPolicy
}

func (s *Store) SetGPUPowerPolicy(policy protocol.GPUPowerPolicy) error {
	if policy.MaxPowerW < 0 || policy.MaxPowerW > 600 {
		return errors.New("maximum GPU power must be between 0 and 600 watts per GPU")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.GPUPowerPolicy == policy {
		return nil
	}
	s.state.GPUPowerPolicy = policy
	for _, rig := range s.state.Rigs {
		if len(rig.Metrics.GPUs) != 0 {
			rig.Desired.Revision++
		}
	}
	s.recordLocked("update", "gpu-power-policy", "controller", map[string]any{"max_power_w": policy.MaxPowerW})
	return s.saveLocked()
}

func applyGPUPowerPolicyToProfile(profile protocol.OverclockProfile, maximum int, gpus []protocol.GPU) protocol.OverclockProfile {
	if maximum <= 0 || len(gpus) == 0 {
		return profile
	}
	if profile.ID == "" {
		profile.ID = "gpu-power-policy"
		profile.Name = "Controller GPU power policy"
		profile.Vendor = gpus[0].Vendor
	}
	if profile.Vendor == "" {
		profile.Vendor = gpus[0].Vendor
	}
	settings := make([]protocol.GPUOverclock, 0, len(profile.Settings)+1)
	hasWildcard := false
	for _, setting := range profile.Settings {
		if setting.Selector == "*" {
			hasWildcard = true
		}
		if setting.PowerLimitW == 0 || setting.PowerLimitW > maximum {
			setting.PowerLimitW = maximum
		}
		settings = append(settings, setting)
	}
	if !hasWildcard {
		settings = append([]protocol.GPUOverclock{{Selector: "*", PowerLimitW: maximum}}, settings...)
	}
	profile.Settings = settings
	return profile
}

func (s *Store) resolveWorkloadLocked(rig *storedRig, sheet protocol.FlightSheet) protocol.ResolvedWorkload {
	result := protocol.ResolvedWorkload{
		Coin: sheet.Coin, Algorithm: sheet.Algorithm,
		Miner:      s.state.Miners[sheet.MinerID],
		Wallet:     s.state.Wallets[sheet.WalletID],
		Pool:       s.state.Pools[sheet.PoolID],
		WorkerName: sheet.WorkerName,
	}
	if sheet.PoolURLOverride != "" {
		result.Pool.URL = sheet.PoolURLOverride
	}
	if sheet.PoolPasswordOverride != "" {
		result.Pool.Password = sheet.PoolPasswordOverride
	}
	if result.Miner.CatalogID == "srbminer-multi" {
		if sheet.SecondaryCoin == "" {
			result.Miner.DefaultArguments = srbSingleWorkloadArguments(sheet.DeviceType, sheet.Algorithm, result.Pool, result.Wallet, sheet.WalletTemplate)
		} else {
			secondaryWallet := s.state.Wallets[sheet.SecondaryWalletID]
			secondaryPool := s.state.Pools[sheet.SecondaryPoolID]
			result.Miner.DefaultArguments = append(
				srbWorkloadArguments(sheet.DeviceType, sheet.Algorithm, result.Pool, result.Wallet, sheet.WalletTemplate),
				srbWorkloadArguments(sheet.SecondaryDeviceType, sheet.SecondaryAlgorithm, secondaryPool, secondaryWallet, "")...,
			)
		}
		result.Miner.DefaultArguments = append(result.Miner.DefaultArguments,
			"--api-enable", "--api-port", "21550", "--api-rig-name", "{WORKER}",
		)
		result.Miner.ExtraArguments = nil
	} else if sheet.Algorithm != "" {
		algorithm := catalogAlgorithmForCoin(result.Miner.CatalogID, sheet.Coin, sheet.Algorithm)
		result.Algorithm = algorithm
		result.Miner.Algorithm = algorithm
		if entry, ok := catalogEntry(result.Miner.CatalogID); ok && entry.AlgorithmFlag != "" {
			if result.Miner.CatalogID == "miniz" {
				algorithm = miniZAlgorithmParameter(algorithm)
			}
			result.Miner.ExtraArguments = append(append([]string(nil), result.Miner.ExtraArguments...), entry.AlgorithmFlag, algorithm)
		}
	}
	if result.Miner.CatalogID == "epic-miner" && !hasArgument(result.Miner.DefaultArguments, "--stratum-login") &&
		!hasArgument(result.Miner.ExtraArguments, "--stratum-login") && !hasArgument(sheet.ExtraArguments, "--stratum-login") {
		result.Miner.DefaultArguments = append(append([]string(nil), result.Miner.DefaultArguments...),
			"--stratum-login", "{STRATUM_LOGIN}",
		)
	}
	if sheet.WalletTemplate != "" {
		workerName := sheet.WorkerName
		if workerName == "" {
			workerName = rig.Rig.Name
		}
		result.MiningUser = renderWalletTemplate(sheet.WalletTemplate, result.Wallet.Address, workerName)
		for index, argument := range result.Miner.DefaultArguments {
			result.Miner.DefaultArguments[index] = strings.ReplaceAll(argument, "{WALLET}.{WORKER}", "{WALLET}")
		}
	}
	result.Miner.ExtraArguments = append(result.Miner.ExtraArguments, sheet.ExtraArguments...)
	return result
}

func miniZAlgorithmParameter(algorithm string) string {
	left, right, ok := strings.Cut(algorithm, ".")
	if !ok || left == "" || right == "" {
		return algorithm
	}
	for _, part := range []string{left, right} {
		for _, character := range part {
			if character < '0' || character > '9' {
				return algorithm
			}
		}
	}
	return left + "," + right
}

func hasArgument(arguments []string, name string) bool {
	for _, argument := range arguments {
		if argument == name || strings.HasPrefix(argument, name+"=") {
			return true
		}
	}
	return false
}

func validMiningEndpoint(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch parsed.Scheme {
	case "stratum+tcp", "stratum+ssl", "stratum+tls", "stratum+tcps", "stratum1+tcp", "stratum1+ssl", "stratum1+tls", "stratum1+tcps", "http", "https", "ws", "wss":
		return true
	default:
		return false
	}
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func renderWalletTemplate(template, wallet, worker string) string {
	return strings.NewReplacer(
		"%WAL%", wallet,
		"%WORKER_NAME%", worker,
		"{WALLET}", wallet,
		"{WORKER}", worker,
	).Replace(template)
}

func srbWorkloadArguments(deviceType, algorithm string, pool protocol.Pool, wallet protocol.Wallet, walletTemplate string) []string {
	suffix := strings.ToLower(deviceType)
	return []string{
		"--algorithm-" + suffix, algorithm,
		"--pool-" + suffix, pool.URL,
		"--wallet-" + suffix, srbWalletArgument(wallet, walletTemplate),
		"--password-" + suffix, pool.Password,
	}
}

func srbSingleWorkloadArguments(deviceType, algorithm string, pool protocol.Pool, wallet protocol.Wallet, walletTemplate string) []string {
	arguments := []string{
		"--algorithm", algorithm,
		"--pool", pool.URL,
		"--wallet", srbWalletArgument(wallet, walletTemplate),
		"--password", pool.Password,
	}
	if strings.EqualFold(deviceType, "CPU") {
		return append(arguments, "--disable-gpu")
	}
	if strings.EqualFold(deviceType, "GPU") {
		return append(arguments, "--disable-cpu")
	}
	return arguments
}

func srbWalletArgument(wallet protocol.Wallet, walletTemplate string) string {
	if walletTemplate != "" {
		return "{WALLET}"
	}
	return wallet.Address + ".{WORKER}"
}

func (s *Store) ListActivities(limit int) []protocol.Activity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	start := len(s.state.Activities) - limit
	if start < 0 {
		start = 0
	}
	result := append([]protocol.Activity(nil), s.state.Activities[start:]...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func (s *Store) DeleteResource(kind, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case "farms":
		if _, ok := s.state.Farms[id]; !ok {
			return ErrNotFound
		}
		for _, rig := range s.state.Rigs {
			if rig.FarmID == id {
				return errors.New("farm is assigned to a worker")
			}
		}
		delete(s.state.Farms, id)
	case "wallets":
		if _, ok := s.state.Wallets[id]; !ok {
			return ErrNotFound
		}
		for _, sheet := range s.state.FlightSheets {
			if sheet.WalletID == id || sheet.SecondaryWalletID == id {
				return errors.New("wallet is used by a flight sheet")
			}
		}
		delete(s.state.Wallets, id)
	case "pools":
		if _, ok := s.state.Pools[id]; !ok {
			return ErrNotFound
		}
		for _, sheet := range s.state.FlightSheets {
			if sheet.PoolID == id || sheet.SecondaryPoolID == id {
				return errors.New("pool is used by a flight sheet")
			}
		}
		delete(s.state.Pools, id)
	case "miners":
		if _, ok := s.state.Miners[id]; !ok {
			return ErrNotFound
		}
		for _, sheet := range s.state.FlightSheets {
			if sheet.MinerID == id {
				return errors.New("miner is used by a flight sheet")
			}
		}
		delete(s.state.Miners, id)
		for _, suffix := range []string{".bin", ".tar.gz"} {
			if err := os.Remove(filepath.Join(s.packageDir, id+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	case "flight-sheets":
		if _, ok := s.state.FlightSheets[id]; !ok {
			return ErrNotFound
		}
		for _, sheet := range s.state.FlightSheets {
			if sheet.FallbackFlightSheetID == id {
				return errors.New("flight sheet is used as an automatic fallback")
			}
		}
		for _, rig := range s.state.Rigs {
			if rig.Desired.FlightSheetID == id {
				return errors.New("flight sheet is assigned to a worker")
			}
		}
		for _, schedule := range s.state.Schedules {
			if schedule.FlightSheetID == id {
				return errors.New("flight sheet is used by a schedule")
			}
		}
		delete(s.state.FlightSheets, id)
	case "overclock-profiles":
		if _, ok := s.state.OverclockProfiles[id]; !ok {
			return ErrNotFound
		}
		for _, rig := range s.state.Rigs {
			if rig.Desired.OverclockProfileID == id {
				return errors.New("overclock profile is assigned to a worker")
			}
		}
		delete(s.state.OverclockProfiles, id)
	case "schedules":
		if _, ok := s.state.Schedules[id]; !ok {
			return ErrNotFound
		}
		delete(s.state.Schedules, id)
	default:
		return ErrNotFound
	}
	s.recordLocked("delete", strings.TrimSuffix(kind, "s"), id, nil)
	return s.saveLocked()
}

func (s *Store) recordLocked(action, resource, resourceID string, details map[string]any) {
	s.state.Activities = append(s.state.Activities, protocol.Activity{
		ID: mustToken(10), At: time.Now().UTC(), Action: action,
		Resource: resource, ResourceID: resourceID, Details: details,
	})
	if len(s.state.Activities) > 2000 {
		s.state.Activities = append([]protocol.Activity(nil), s.state.Activities[len(s.state.Activities)-2000:]...)
	}
}

func (s *Store) bumpWorkersForResourceLocked(kind, id string) {
	for _, rig := range s.state.Rigs {
		switch kind {
		case "flight-sheet":
			sheet, assigned := s.state.FlightSheets[rig.Desired.FlightSheetID]
			if rig.Desired.FlightSheetID == id || (assigned && sheet.FallbackFlightSheetID == id) {
				rig.Desired.Revision++
			}
		case "overclock-profile":
			if rig.Desired.OverclockProfileID == id {
				rig.Desired.Revision++
			}
		case "wallet", "pool", "miner":
			sheet, ok := s.state.FlightSheets[rig.Desired.FlightSheetID]
			if !ok {
				continue
			}
			fallback := s.state.FlightSheets[sheet.FallbackFlightSheetID]
			if (kind == "wallet" && (sheet.WalletID == id || sheet.SecondaryWalletID == id || fallback.WalletID == id || fallback.SecondaryWalletID == id)) ||
				(kind == "pool" && (sheet.PoolID == id || sheet.SecondaryPoolID == id || fallback.PoolID == id || fallback.SecondaryPoolID == id)) ||
				(kind == "miner" && (sheet.MinerID == id || fallback.MinerID == id)) {
				rig.Desired.Revision++
			}
		}
	}
}

func sortedValues[T any](values map[string]T, key func(T) string) []T {
	result := make([]T, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(key(result[i])) < strings.ToLower(key(result[j])) })
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func isHex(value string) bool {
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

func mustToken(size int) string {
	value, err := randomToken(size)
	if err != nil {
		panic(err)
	}
	return value
}
