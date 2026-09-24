package protocol

import "time"

type GPU struct {
	Index       int     `json:"index"`
	Vendor      string  `json:"vendor"`
	Name        string  `json:"name"`
	UUID        string  `json:"uuid,omitempty"`
	PCIAddress  string  `json:"pci_address,omitempty"`
	Driver      string  `json:"driver,omitempty"`
	Firmware    string  `json:"firmware,omitempty"`
	Temperature float64 `json:"temperature_c"`
	Power       float64 `json:"power_w"`
	Fan         float64 `json:"fan_percent,omitempty"`
	Utilization float64 `json:"utilization_percent"`
	MemoryUsed  uint64  `json:"memory_used_bytes"`
	MemoryTotal uint64  `json:"memory_total_bytes"`
	CoreClock   float64 `json:"core_clock_mhz,omitempty"`
	MemoryClock float64 `json:"memory_clock_mhz,omitempty"`
}

type Metrics struct {
	CollectedAt           time.Time        `json:"collected_at"`
	Uptime                uint64           `json:"uptime_seconds"`
	CPUUsage              float64          `json:"cpu_usage_percent"`
	CPUTemperature        float64          `json:"cpu_temperature_c,omitempty"`
	Load1                 float64          `json:"load_1"`
	MemoryUsed            uint64           `json:"memory_used_bytes"`
	MemoryTotal           uint64           `json:"memory_total_bytes"`
	GPUs                  []GPU            `json:"gpus"`
	Miner                 MinerState       `json:"miner"`
	SmartTune             *SmartTuneStatus `json:"smart_tune,omitempty"`
	SmartTuneSupported    bool             `json:"smart_tune_supported,omitempty"`
	DriverUpdateSupported bool             `json:"driver_update_supported,omitempty"`
	MiningCoin            string           `json:"mining_coin,omitempty"`
	MiningAlgorithm       string           `json:"mining_algorithm,omitempty"`
	AppliedRevision       uint64           `json:"applied_revision"`
	System                SystemInfo       `json:"system"`
}

type SystemInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Kernel   string `json:"kernel"`
	CPUModel string `json:"cpu_model"`
	CPUCores int    `json:"cpu_cores"`
	Agent    string `json:"agent_version"`
}

type MinerState struct {
	Profile        string           `json:"profile,omitempty"`
	Running        bool             `json:"running"`
	PID            int              `json:"pid,omitempty"`
	Hashrate       float64          `json:"hashrate,omitempty"`
	HashrateUnit   string           `json:"hashrate_unit,omitempty"`
	AcceptedShares uint64           `json:"accepted_shares,omitempty"`
	RejectedShares uint64           `json:"rejected_shares,omitempty"`
	LastError      string           `json:"last_error,omitempty"`
	Devices        []DeviceHashrate `json:"devices,omitempty"`
}

type DeviceHashrate struct {
	Index       int     `json:"index"`
	Kind        string  `json:"kind,omitempty"`
	Name        string  `json:"name,omitempty"`
	Hashrate    float64 `json:"hashrate"`
	Unit        string  `json:"unit"`
	Temperature float64 `json:"temperature_c,omitempty"`
}

type EnrollRequest struct {
	Name            string `json:"name"`
	EnrollmentToken string `json:"enrollment_token"`
}

type EnrollResponse struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

type HeartbeatRequest struct {
	Metrics Metrics `json:"metrics"`
}

type Rig struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	EnrolledAt       time.Time                  `json:"enrolled_at"`
	LastHeartbeat    time.Time                  `json:"last_heartbeat"`
	IPAddress        string                     `json:"ip_address,omitempty"`
	Online           bool                       `json:"online"`
	Metrics          Metrics                    `json:"metrics"`
	DriverEfficiency []DriverEfficiencySummary  `json:"driver_efficiency,omitempty"`
	OverclockPresets []OverclockKnowledgePreset `json:"overclock_presets,omitempty"`
	FarmID           string                     `json:"farm_id,omitempty"`
	Tags             []string                   `json:"tags"`
	Desired          DesiredConfiguration       `json:"desired"`
}

type DriverEfficiencyRecord struct {
	Vendor        string       `json:"vendor"`
	Model         string       `json:"model"`
	Driver        string       `json:"driver"`
	Coin          string       `json:"coin"`
	Algorithm     string       `json:"algorithm"`
	HashrateUnit  string       `json:"hashrate_unit"`
	Miner         string       `json:"miner,omitempty"`
	Tuning        GPUOverclock `json:"tuning"`
	Samples       uint64       `json:"samples"`
	TotalHashrate float64      `json:"total_hashrate"`
	TotalPowerW   float64      `json:"total_power_w"`
	FirstSampleAt time.Time    `json:"first_sample_at"`
	LastSampleAt  time.Time    `json:"last_sample_at"`
}

type OverclockKnowledgePreset struct {
	GPUIndex     int          `json:"gpu_index"`
	Vendor       string       `json:"vendor"`
	Model        string       `json:"model"`
	Driver       string       `json:"driver"`
	Coin         string       `json:"coin"`
	Algorithm    string       `json:"algorithm"`
	Miner        string       `json:"miner,omitempty"`
	HashrateUnit string       `json:"hashrate_unit"`
	Efficiency   float64      `json:"efficiency"`
	Samples      uint64       `json:"samples"`
	Tuning       GPUOverclock `json:"tuning"`
}

type DriverEfficiencySummary struct {
	GPUIndex           int     `json:"gpu_index"`
	Vendor             string  `json:"vendor"`
	Model              string  `json:"model"`
	Coin               string  `json:"coin"`
	Algorithm          string  `json:"algorithm"`
	HashrateUnit       string  `json:"hashrate_unit"`
	CurrentDriver      string  `json:"current_driver"`
	CurrentEfficiency  float64 `json:"current_efficiency,omitempty"`
	CurrentSamples     uint64  `json:"current_samples"`
	BestDriver         string  `json:"best_driver,omitempty"`
	BestEfficiency     float64 `json:"best_efficiency,omitempty"`
	BestSamples        uint64  `json:"best_samples,omitempty"`
	ComparedDrivers    int     `json:"compared_drivers"`
	ImprovementPercent float64 `json:"improvement_percent,omitempty"`
	Status             string  `json:"status"`
}

type Command struct {
	ID           string               `json:"id"`
	Action       string               `json:"action"`
	Profile      string               `json:"profile,omitempty"`
	SmartTune    *SmartTuneRequest    `json:"smart_tune,omitempty"`
	DriverUpdate *DriverUpdateRequest `json:"driver_update,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	Status       string               `json:"status"`
	Error        string               `json:"error,omitempty"`
	Output       string               `json:"output,omitempty"`
	Attempt      int                  `json:"attempt"`
	LeaseUntil   time.Time            `json:"lease_until,omitempty"`
}

type CommandRequest struct {
	Action       string               `json:"action"`
	Profile      string               `json:"profile,omitempty"`
	SmartTune    *SmartTuneRequest    `json:"smart_tune,omitempty"`
	DriverUpdate *DriverUpdateRequest `json:"driver_update,omitempty"`
}

type DriverUpdateRequest struct {
	Vendor  string `json:"vendor"`
	Channel string `json:"channel"`
}

type BulkCommandRequest struct {
	RigIDs  []string `json:"rig_ids"`
	Action  string   `json:"action"`
	Profile string   `json:"profile,omitempty"`
}

type CommandResult struct {
	Error         string         `json:"error,omitempty"`
	Output        string         `json:"output,omitempty"`
	TunedSettings []GPUOverclock `json:"tuned_settings,omitempty"`
}

type SmartTuneRequest struct {
	Mode            string  `json:"mode"`
	MaxPowerW       int     `json:"max_power_w"`
	MaxTemperatureC float64 `json:"max_temperature_c"`
}

type SmartTuneStatus struct {
	Active           bool           `json:"active"`
	CommandID        string         `json:"command_id,omitempty"`
	Phase            string         `json:"phase,omitempty"`
	GPUIndex         int            `json:"gpu_index,omitempty"`
	CompletedGPUs    int            `json:"completed_gpus,omitempty"`
	TotalGPUs        int            `json:"total_gpus,omitempty"`
	Message          string         `json:"message,omitempty"`
	StartedAt        time.Time      `json:"started_at,omitempty"`
	SelectedSettings []GPUOverclock `json:"selected_settings,omitempty"`
}

type Farm struct {
	ID                       string    `json:"id"`
	Name                     string    `json:"name"`
	Description              string    `json:"description,omitempty"`
	ElectricityRateUSDPerKWh float64   `json:"electricity_rate_usd_per_kwh,omitempty"`
	CreatedAt                time.Time `json:"created_at"`
}

type Wallet struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Coin      string    `json:"coin"`
	Address   string    `json:"address"`
	CreatedAt time.Time `json:"created_at"`
}

type Pool struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	Password     string    `json:"password,omitempty"`
	DashboardURL string    `json:"dashboard_url,omitempty"`
	StatsURL     string    `json:"stats_url,omitempty"`
	StatsPoolID  string    `json:"stats_pool_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type MinerDefinition struct {
	ID               string            `json:"id"`
	CatalogID        string            `json:"catalog_id,omitempty"`
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Profile          string            `json:"profile"`
	Algorithm        string            `json:"algorithm"`
	ExtraArguments   []string          `json:"extra_arguments"`
	SourceURL        string            `json:"source_url,omitempty"`
	SourceCommit     string            `json:"source_commit,omitempty"`
	BinarySHA256     string            `json:"binary_sha256,omitempty"`
	PackageSHA256    string            `json:"package_sha256,omitempty"`
	PackageFormat    string            `json:"package_format,omitempty"`
	EntryPoint       string            `json:"entry_point,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	StatsType        string            `json:"stats_type,omitempty"`
	StatsURL         string            `json:"stats_url,omitempty"`
	ManagedBinary    bool              `json:"managed_binary"`
	BinaryName       string            `json:"binary_name,omitempty"`
	DefaultArguments []string          `json:"default_arguments"`
	CreatedAt        time.Time         `json:"created_at"`
}

type MinerCatalogEntry struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	Hardware           []string  `json:"hardware"`
	License            string    `json:"license"`
	Redistribution     string    `json:"redistribution"`
	SourceURL          string    `json:"source_url"`
	BinaryName         string    `json:"binary_name"`
	DefaultArguments   []string  `json:"default_arguments"`
	AlgorithmFlag      string    `json:"algorithm_flag"`
	PackageRequired    bool      `json:"package_required"`
	ImageIncluded      bool      `json:"image_included"`
	AutomaticInstall   bool      `json:"automatic_install"`
	InstalledMinerID   string    `json:"installed_miner_id,omitempty"`
	InstalledVersion   string    `json:"installed_version,omitempty"`
	LatestVersion      string    `json:"latest_version,omitempty"`
	LatestReleaseURL   string    `json:"latest_release_url,omitempty"`
	LatestReleaseNotes string    `json:"latest_release_notes,omitempty"`
	LatestPublishedAt  time.Time `json:"latest_published_at,omitempty"`
	UpdateAvailable    bool      `json:"update_available"`
	ReleaseCheckedAt   time.Time `json:"release_checked_at,omitempty"`
	ReleaseCheckError  string    `json:"release_check_error,omitempty"`
	PackageReady       bool      `json:"package_ready"`
}

type CustomMinerImport struct {
	CatalogID             string            `json:"-"`
	Name                  string            `json:"name"`
	Version               string            `json:"version,omitempty"`
	Algorithm             string            `json:"algorithm"`
	URL                   string            `json:"url"`
	BinaryName            string            `json:"binary_name"`
	PackageFormat         string            `json:"package_format,omitempty"`
	EntryPoint            string            `json:"entry_point,omitempty"`
	Environment           map[string]string `json:"environment,omitempty"`
	StatsType             string            `json:"stats_type,omitempty"`
	StatsURL              string            `json:"stats_url,omitempty"`
	ExpectedBinarySHA256  string            `json:"expected_binary_sha256,omitempty"`
	ExpectedPackageSHA256 string            `json:"expected_package_sha256,omitempty"`
	DefaultArguments      []string          `json:"default_arguments"`
}

type CommunityMiner struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Description          string            `json:"description,omitempty"`
	Hardware             []string          `json:"hardware,omitempty"`
	License              string            `json:"license,omitempty"`
	SourceURL            string            `json:"source_url"`
	PackageURL           string            `json:"package_url"`
	ExecutableName       string            `json:"executable_name"`
	ExecutableSHA256     string            `json:"executable_sha256"`
	PackageSHA256        string            `json:"package_sha256,omitempty"`
	PackageFormat        string            `json:"package_format,omitempty"`
	EntryPoint           string            `json:"entry_point,omitempty"`
	Environment          map[string]string `json:"environment,omitempty"`
	StatsType            string            `json:"stats_type,omitempty"`
	StatsURL             string            `json:"stats_url,omitempty"`
	DefaultArguments     []string          `json:"default_arguments"`
	RecommendedAlgorithm string            `json:"recommended_algorithm,omitempty"`
}

type CommunityMinerCatalog struct {
	SchemaVersion int              `json:"schema_version"`
	RepositoryURL string           `json:"repository_url,omitempty"`
	UpdatedAt     time.Time        `json:"updated_at,omitempty"`
	Miners        []CommunityMiner `json:"miners"`
	Error         string           `json:"error,omitempty"`
}

type RigOSStatus struct {
	ImageAvailable     bool                `json:"image_available"`
	ImageFilename      string              `json:"image_filename"`
	ImageSizeBytes     int64               `json:"image_size_bytes,omitempty"`
	ImageSHA256        string              `json:"image_sha256,omitempty"`
	ImageDownloadURL   string              `json:"image_download_url,omitempty"`
	ReleaseVersion     string              `json:"release_version,omitempty"`
	ReleaseURL         string              `json:"release_url,omitempty"`
	ReleaseImageURL    string              `json:"release_image_url,omitempty"`
	ReleaseChecksumURL string              `json:"release_checksum_url,omitempty"`
	SupportsUSBRun     bool                `json:"supports_usb_run"`
	SupportsLocalDrive bool                `json:"supports_local_drive"`
	Miners             []MinerCatalogEntry `json:"miners"`
}

type FlightSheet struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Coin                 string    `json:"coin"`
	DeviceType           string    `json:"device_type,omitempty"`
	Algorithm            string    `json:"algorithm,omitempty"`
	WalletID             string    `json:"wallet_id"`
	PoolID               string    `json:"pool_id"`
	MinerID              string    `json:"miner_id"`
	SecondaryCoin        string    `json:"secondary_coin,omitempty"`
	SecondaryDeviceType  string    `json:"secondary_device_type,omitempty"`
	SecondaryAlgorithm   string    `json:"secondary_algorithm,omitempty"`
	SecondaryWalletID    string    `json:"secondary_wallet_id,omitempty"`
	SecondaryPoolID      string    `json:"secondary_pool_id,omitempty"`
	PoolURLOverride      string    `json:"pool_url_override,omitempty"`
	BackupPoolURLs       []string  `json:"backup_pool_urls,omitempty"`
	PoolPasswordOverride string    `json:"pool_password_override,omitempty"`
	WalletTemplate       string    `json:"wallet_template,omitempty"`
	WorkerName           string    `json:"worker_name,omitempty"`
	ExtraArguments       []string  `json:"extra_arguments,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type QuickFlightSheetInput struct {
	Name                   string   `json:"name"`
	DeviceType             string   `json:"device_type"`
	Coin                   string   `json:"coin"`
	WalletAddress          string   `json:"wallet_address"`
	PoolURL                string   `json:"pool_url"`
	PoolPassword           string   `json:"pool_password,omitempty"`
	MinerID                string   `json:"miner_id,omitempty"`
	CatalogID              string   `json:"catalog_id,omitempty"`
	Algorithm              string   `json:"algorithm"`
	WalletTemplate         string   `json:"wallet_template,omitempty"`
	RigIDs                 []string `json:"rig_ids,omitempty"`
	SecondaryCoin          string   `json:"secondary_coin,omitempty"`
	SecondaryDeviceType    string   `json:"secondary_device_type,omitempty"`
	SecondaryWalletAddress string   `json:"secondary_wallet_address,omitempty"`
	SecondaryPoolURL       string   `json:"secondary_pool_url,omitempty"`
	SecondaryPoolPassword  string   `json:"secondary_pool_password,omitempty"`
	SecondaryAlgorithm     string   `json:"secondary_algorithm,omitempty"`
}

type QuickFlightSheetResult struct {
	FlightSheet FlightSheet `json:"flight_sheet"`
	MinerReady  bool        `json:"miner_ready"`
	Assigned    int         `json:"assigned"`
}

type GPUOverclock struct {
	Selector        string `json:"selector"`
	CoreClockMHz    int    `json:"core_clock_mhz,omitempty"`
	CoreOffsetMHz   int    `json:"core_offset_mhz,omitempty"`
	MemoryClockMHz  int    `json:"memory_clock_mhz,omitempty"`
	MemoryOffsetMHz int    `json:"memory_offset_mhz,omitempty"`
	PowerLimitW     int    `json:"power_limit_w,omitempty"`
	FanPercent      int    `json:"fan_percent,omitempty"`
}

type OverclockProfile struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Vendor    string         `json:"vendor"`
	Settings  []GPUOverclock `json:"settings"`
	CreatedAt time.Time      `json:"created_at"`
}

type WatchdogPolicy struct {
	Enabled             bool    `json:"enabled"`
	MaxTemperatureC     float64 `json:"max_temperature_c,omitempty"`
	MinHashrate         float64 `json:"min_hashrate,omitempty"`
	RestartAfterSeconds int     `json:"restart_after_seconds,omitempty"`
	RebootAfterFailures int     `json:"reboot_after_failures,omitempty"`
}

type DesiredConfiguration struct {
	Revision           uint64         `json:"revision"`
	FlightSheetID      string         `json:"flight_sheet_id,omitempty"`
	OverclockProfileID string         `json:"overclock_profile_id,omitempty"`
	EstimatedPowerW    float64        `json:"estimated_power_w,omitempty"`
	PowerOffsetW       float64        `json:"power_offset_w,omitempty"`
	Watchdog           WatchdogPolicy `json:"watchdog"`
	Autofan            AutofanPolicy  `json:"autofan"`
}

type ResolvedConfiguration struct {
	Revision   uint64           `json:"revision"`
	Miner      MinerDefinition  `json:"miner"`
	Wallet     Wallet           `json:"wallet"`
	Pool       Pool             `json:"pool"`
	MiningUser string           `json:"mining_user,omitempty"`
	WorkerName string           `json:"worker_name,omitempty"`
	Overclock  OverclockProfile `json:"overclock"`
	Watchdog   WatchdogPolicy   `json:"watchdog"`
	Autofan    AutofanPolicy    `json:"autofan"`
}

type WorkerAssignment struct {
	Name               string         `json:"name,omitempty"`
	FarmID             string         `json:"farm_id,omitempty"`
	Tags               []string       `json:"tags,omitempty"`
	FlightSheetID      string         `json:"flight_sheet_id,omitempty"`
	OverclockProfileID string         `json:"overclock_profile_id,omitempty"`
	EstimatedPowerW    float64        `json:"estimated_power_w,omitempty"`
	PowerOffsetW       float64        `json:"power_offset_w,omitempty"`
	Watchdog           WatchdogPolicy `json:"watchdog"`
	Autofan            AutofanPolicy  `json:"autofan"`
}

type AutofanPolicy struct {
	Enabled            bool    `json:"enabled"`
	TargetTemperatureC float64 `json:"target_temperature_c,omitempty"`
	MinimumFanPercent  int     `json:"minimum_fan_percent,omitempty"`
	MaximumFanPercent  int     `json:"maximum_fan_percent,omitempty"`
}

type Activity struct {
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	Action     string         `json:"action"`
	Resource   string         `json:"resource"`
	ResourceID string         `json:"resource_id,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

type Alert struct {
	ID         string     `json:"id"`
	RigID      string     `json:"rig_id"`
	Type       string     `json:"type"`
	Severity   string     `json:"severity"`
	Message    string     `json:"message"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	Active     bool       `json:"active"`
}

type Schedule struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	Days          []int     `json:"days"`
	Time          string    `json:"time"`
	Action        string    `json:"action"`
	RigIDs        []string  `json:"rig_ids"`
	FlightSheetID string    `json:"flight_sheet_id,omitempty"`
	LastRunMinute string    `json:"last_run_minute,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

type UserInput struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type PasswordRecoveryRequest struct {
	Username     string `json:"username"`
	RecoveryCode string `json:"recovery_code"`
	NewPassword  string `json:"new_password"`
}
