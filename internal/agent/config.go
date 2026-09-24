package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
)

type Profile struct {
	Command      string            `json:"command"`
	Args         []string          `json:"args"`
	WorkingDir   string            `json:"working_dir,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	StatsType    string            `json:"stats_type,omitempty"`
	StatsURL     string            `json:"stats_url,omitempty"`
	StatsToken   string            `json:"stats_token,omitempty"`
	StatsCommand string            `json:"stats_command,omitempty"`
	StatsArgs    []string          `json:"stats_args,omitempty"`
}

type Config struct {
	Controller      string             `json:"controller"`
	TLSFingerprint  string             `json:"tls_fingerprint"`
	EnrollmentToken string             `json:"enrollment_token,omitempty"`
	Name            string             `json:"name"`
	StateFile       string             `json:"state_file"`
	PackageDir      string             `json:"package_dir,omitempty"`
	Profiles        map[string]Profile `json:"profiles"`
}

type Identity struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	decoder := json.NewDecoder(bytesNewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if config.Controller == "" || config.TLSFingerprint == "" || config.Name == "" {
		return Config{}, errors.New("controller, tls_fingerprint, and name are required")
	}
	if config.StateFile == "" {
		config.StateFile = "/var/lib/minerdash/identity.json"
	}
	if config.PackageDir == "" {
		config.PackageDir = "/opt/minerdash/miners"
	}
	if !filepath.IsAbs(config.PackageDir) {
		return Config{}, errors.New("package_dir must be an absolute path")
	}
	for name, profile := range config.Profiles {
		if name == "" || !filepath.IsAbs(profile.Command) {
			return Config{}, fmt.Errorf("profile %q must have an absolute command path", name)
		}
		if err := validateStatsEndpoint(profile); err != nil {
			return Config{}, fmt.Errorf("profile %q: %w", name, err)
		}
	}
	return config, nil
}

func validateStatsEndpoint(profile Profile) error {
	if profile.StatsType == "" && profile.StatsURL == "" && profile.StatsCommand == "" {
		return nil
	}
	if profile.StatsType == "command" {
		if !filepath.IsAbs(profile.StatsCommand) {
			return errors.New("stats_command must be an absolute path")
		}
		return nil
	}
	if profile.StatsType != "xmrig" && profile.StatsType != "miniz" && profile.StatsType != "srbminer" {
		return errors.New("stats_type must be xmrig, miniz, srbminer, or command")
	}
	parsed, err := url.Parse(profile.StatsURL)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		return errors.New("stats_url must be a local HTTP URL")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("stats_url must use localhost or a loopback address")
	}
	return nil
}

func LoadIdentity(path string) (Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, err
	}
	var identity Identity
	if err := json.Unmarshal(data, &identity); err != nil {
		return Identity{}, err
	}
	if identity.AgentID == "" || identity.Token == "" {
		return Identity{}, errors.New("identity file is incomplete")
	}
	return identity, nil
}

func SaveIdentity(path string, identity Identity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
