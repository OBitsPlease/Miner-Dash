package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"minerdash/internal/protocol"
)

type Client struct {
	config                 Config
	identity               Identity
	http                   *http.Client
	miner                  *MinerManager
	metrics                MetricsCollector
	logger                 *log.Logger
	appliedRevision        atomic.Uint64
	configurationErr       string
	configuration          protocol.ResolvedConfiguration
	watchdogStopped        bool
	lastFanPercent         map[string]int
	lastPowerLimitEnforced time.Time
	lowHashSince           time.Time
	watchdogFailures       int
	pendingResult          *pendingCommandResult
	tuner                  *smartTuner
	failover               epicFailoverState
	cpuOptimizer           *cpuOptimizer
}

type pendingCommandResult struct {
	commandID string
	result    protocol.CommandResult
}

func NewClient(config Config, logger *log.Logger) (*Client, error) {
	expected := normalizeFingerprint(config.TLSFingerprint)
	if len(expected) != sha256.Size*2 {
		return nil, errors.New("tls_fingerprint must be a SHA-256 certificate fingerprint")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Certificate pinning performs verification without relying on a public CA.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("controller supplied no certificate")
			}
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) != expected {
				return errors.New("controller certificate fingerprint mismatch")
			}
			return nil
		},
	}}
	return &Client{
		config: config, http: &http.Client{Transport: transport, Timeout: 15 * time.Second},
		miner: NewMinerManager(config.Profiles), logger: logger, lastFanPercent: make(map[string]int),
		tuner:        &smartTuner{},
		cpuOptimizer: newCPUOptimizer(config.CPUOptimization),
	}, nil
}

func (c *Client) Run(ctx context.Context) error {
	identity, err := LoadIdentity(c.config.StateFile)
	if errors.Is(err, osErrNotExist) {
		identity, err = c.enroll(ctx)
		if err == nil {
			err = SaveIdentity(c.config.StateFile, identity)
		}
	}
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}
	c.identity = identity
	heartbeatTicker := time.NewTicker(10 * time.Second)
	commandTicker := time.NewTicker(2 * time.Second)
	configurationTicker := time.NewTicker(10 * time.Second)
	failoverTicker := time.NewTicker(5 * time.Second)
	updateTicker := time.NewTicker(6 * time.Hour)
	defer heartbeatTicker.Stop()
	defer commandTicker.Stop()
	defer configurationTicker.Stop()
	defer failoverTicker.Stop()
	defer updateTicker.Stop()
	if err := c.syncAndRecordConfiguration(ctx); err != nil {
		c.logger.Printf("initial configuration sync failed: %v", err)
	}
	if err := c.heartbeat(ctx); err != nil {
		c.logger.Printf("initial heartbeat failed: %v", err)
	}
	if err := c.checkAgentUpdate(ctx); err != nil {
		c.logger.Printf("agent update check failed: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeatTicker.C:
			if err := c.heartbeat(ctx); err != nil {
				c.logger.Printf("heartbeat failed: %v", err)
			}
		case <-commandTicker.C:
			if err := c.pollCommand(ctx); err != nil {
				c.logger.Printf("command poll failed: %v", err)
			}
		case <-configurationTicker.C:
			if err := c.syncAndRecordConfiguration(ctx); err != nil {
				c.logger.Printf("configuration sync failed: %v", err)
			}
		case <-failoverTicker.C:
			if err := c.reconcileEpicFailover(ctx); err != nil {
				c.logger.Printf("Epic failover check failed: %v", err)
			}
		case <-updateTicker.C:
			if err := c.checkAgentUpdate(ctx); err != nil {
				c.logger.Printf("agent update check failed: %v", err)
			}
		}
	}
}

func (c *Client) checkAgentUpdate(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	installer := "/usr/local/sbin/minerdash-install-agent-release"
	if info, err := os.Stat(installer); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	releasePath := "/api/v1/agents/" + c.identity.AgentID + "/release/" + runtime.GOARCH
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimRight(c.config.Controller, "/")+releasePath, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.identity.Token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("agent release check returned %s", response.Status)
	}
	expected := strings.ToLower(strings.TrimSpace(response.Header.Get("X-Content-SHA256")))
	if len(expected) != 64 {
		return errors.New("controller agent release is missing a valid SHA-256")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	current, err := fileSHA256(executable)
	if err != nil {
		return err
	}
	if current == expected {
		return nil
	}
	stagedRelease := filepath.Join("/opt/minerdash/agent/releases", expected, "minerdash-agent")
	if _, err := os.Stat(stagedRelease); errors.Is(err, os.ErrNotExist) {
		download := filepath.Join("/var/lib/minerdash", "agent-update-"+expected)
		if err := c.downloadAgentRelease(ctx, releasePath, download, expected); err != nil {
			return err
		}
		defer os.Remove(download)
		output, err := exec.Command(installer, "stage", download, expected).CombinedOutput()
		if err != nil {
			return fmt.Errorf("stage agent update: %w: %s", err, strings.TrimSpace(string(output)))
		}
	} else if err != nil {
		return err
	}
	if err := startDetachedAgentActivation(installer, expected); err != nil {
		return fmt.Errorf("activate agent update: %w", err)
	}
	c.logger.Printf("verified agent update %s activated; restarting service", expected[:12])
	return nil
}

// startDetachedAgentActivation runs the activation helper outside the agent's own
// service scope when systemd-run is available. The helper restarts
// minerdash-agent.service, which terminates every process in that unit's cgroup,
// so running it as a direct child risks killing it mid-restart.
func startDetachedAgentActivation(installer, digest string) error {
	if path, err := exec.LookPath("systemd-run"); err == nil {
		command := exec.Command(path, "--no-block", "--collect",
			"--unit", "minerdash-agent-activate", "--description", "MinerDash agent activation",
			installer, "activate", digest)
		if err := command.Run(); err == nil {
			return nil
		}
	}
	return exec.Command(installer, "activate", digest).Start()
}

func (c *Client) downloadAgentRelease(ctx context.Context, releasePath, destination, expectedHash string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.config.Controller, "/")+releasePath, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.identity.Token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("agent release download returned %s", response.Status)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxManagedMinerSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	if written == 0 || written > maxManagedMinerSize {
		_ = os.Remove(destination)
		return errors.New("agent release is empty or exceeds its size limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedHash {
		_ = os.Remove(destination)
		return errors.New("agent release SHA-256 mismatch")
	}
	return nil
}

func (c *Client) enroll(ctx context.Context) (Identity, error) {
	request := protocol.EnrollRequest{Name: c.config.Name, EnrollmentToken: c.config.EnrollmentToken}
	var response protocol.EnrollResponse
	if err := c.request(ctx, http.MethodPost, "/api/v1/enroll", "", request, &response); err != nil {
		return Identity{}, err
	}
	return Identity{AgentID: response.AgentID, Token: response.Token}, nil
}

func (c *Client) heartbeat(ctx context.Context) error {
	metrics := c.metrics.Collect(c.miner.State())
	if c.configuration.GPUPowerLimitW > 0 && len(metrics.GPUs) > 0 &&
		(time.Since(c.lastPowerLimitEnforced) >= 30*time.Second || c.lastPowerLimitEnforced.IsZero()) {
		if err := enforceGPUPowerLimit(c.configuration.GPUPowerLimitW, metrics.GPUs); err != nil {
			c.configurationErr = err.Error()
			if stopErr := c.miner.Stop(); stopErr != nil {
				c.configurationErr = fmt.Sprintf("%v; stop miner: %v", err, stopErr)
			}
			c.logger.Printf("GPU power policy enforcement failed: %v", err)
		} else {
			c.lastPowerLimitEnforced = time.Now()
		}
	}
	if err := applyAutofan(c.configuration.Autofan, metrics.GPUs, c.lastFanPercent); err != nil {
		c.logger.Printf("autofan failed: %v", err)
	}
	if err := c.enforceWatchdog(metrics); err != nil {
		c.logger.Printf("watchdog action failed: %v", err)
	}
	metrics.Miner = c.miner.State()
	metrics.SmartTune = c.tuner.snapshot()
	metrics.SmartTuneSupported = true
	metrics.DriverUpdateSupported = runtime.GOOS == "linux"
	metrics.AppliedRevision = c.appliedRevision.Load()
	metrics.ConfigurationError = c.configurationErr
	c.applyMiningStatus(&metrics)
	metrics.CPUOptimization = c.cpuOptimizer.update(metrics, c.miner.Logs())
	request := protocol.HeartbeatRequest{Metrics: metrics}
	err := c.request(ctx, http.MethodPost, "/api/v1/agents/"+c.identity.AgentID+"/heartbeat", c.identity.Token, request, nil)
	if errors.Is(err, errNoContent) {
		return nil
	}
	return err
}

func (c *Client) syncAndRecordConfiguration(ctx context.Context) error {
	err := c.syncConfiguration(ctx)
	if err != nil {
		c.configurationErr = err.Error()
		return err
	}
	c.configurationErr = ""
	return nil
}

func (c *Client) syncConfiguration(ctx context.Context) error {
	if c.tuner.active() {
		return nil
	}
	var configuration protocol.ResolvedConfiguration
	err := c.request(ctx, http.MethodGet, "/api/v1/agents/"+c.identity.AgentID+"/configuration", c.identity.Token, nil, &configuration)
	if err != nil {
		return err
	}
	if configuration.Revision <= c.appliedRevision.Load() &&
		configuration.GPUPowerLimitW == c.configuration.GPUPowerLimitW {
		return nil
	}
	if err := validateResolvedConfiguration(configuration); err != nil {
		return err
	}
	if c.configuration.Autofan.Enabled && !configuration.Autofan.Enabled {
		if err := resetAutofan(); err != nil {
			return fmt.Errorf("restore automatic fan control: %w", err)
		}
		clear(c.lastFanPercent)
	}
	previousConfiguration := c.configuration
	binaryUpdate, err := c.ensureManagedMiner(ctx, &configuration)
	if err != nil {
		return err
	}
	var fallbackBinaryUpdate *minerBinaryUpdate
	if configuration.Fallback != nil {
		fallbackConfiguration := protocol.ResolvedConfiguration{Miner: configuration.Fallback.Miner}
		fallbackBinaryUpdate, err = c.ensureManagedMiner(ctx, &fallbackConfiguration)
		if err != nil {
			if binaryUpdate != nil {
				_ = binaryUpdate.rollback()
			}
			return fmt.Errorf("prepare fallback miner: %w", err)
		}
		configuration.Fallback.Miner = fallbackConfiguration.Miner
	}
	gpus := c.metrics.Collect(c.miner.State()).GPUs
	if configuration.GPUPowerLimitW > 0 && len(gpus) > 0 {
		if err := enforceGPUPowerLimit(configuration.GPUPowerLimitW, gpus); err != nil {
			if stopErr := c.miner.Stop(); stopErr != nil {
				return fmt.Errorf("enforce controller GPU power policy: %v; stop miner: %w", err, stopErr)
			}
			return fmt.Errorf("enforce controller GPU power policy: %w", err)
		}
		c.lastPowerLimitEnforced = time.Now()
		configuration.Overclock = applyGPUPowerPolicyToProfile(configuration.Overclock, configuration.GPUPowerLimitW, gpus)
	}
	if err := transitionOverclock(c.configuration.Overclock, configuration.Overclock); err != nil {
		if configuration.GPUPowerLimitW > 0 {
			if enforceErr := enforceGPUPowerLimit(configuration.GPUPowerLimitW, gpus); enforceErr != nil {
				if stopErr := c.miner.Stop(); stopErr != nil {
					return fmt.Errorf("apply overclock: %v; enforce controller GPU power policy: %v; stop miner: %w", err, enforceErr, stopErr)
				}
				return fmt.Errorf("apply overclock: %v; enforce controller GPU power policy: %w", err, enforceErr)
			}
		}
		if binaryUpdate != nil {
			_ = binaryUpdate.rollback()
		}
		if fallbackBinaryUpdate != nil {
			_ = fallbackBinaryUpdate.rollback()
		}
		return err
	}
	if err := c.startResolvedWorkload(configuration); err != nil {
		if fallbackBinaryUpdate != nil {
			_ = fallbackBinaryUpdate.rollback()
		}
		if binaryUpdate == nil {
			return err
		}
		if rollbackErr := binaryUpdate.rollback(); rollbackErr != nil {
			return fmt.Errorf("start updated miner: %v; restore previous binary: %w", err, rollbackErr)
		}
		if previousConfiguration.Miner.Profile == "" {
			return fmt.Errorf("start updated miner: %w; previous binary restored", err)
		}
		if _, prepareErr := c.ensureManagedMiner(ctx, &previousConfiguration); prepareErr != nil {
			return fmt.Errorf("start updated miner: %v; prepare restored miner: %w", err, prepareErr)
		}
		if restartErr := c.startResolvedWorkload(previousConfiguration); restartErr != nil {
			return fmt.Errorf("start updated miner: %v; restart previous miner: %w", err, restartErr)
		}
		return fmt.Errorf("updated miner failed to start and the previous miner was restored: %w", err)
	}
	if binaryUpdate != nil {
		if err := binaryUpdate.commit(); err != nil {
			c.logger.Printf("remove previous miner binary: %v", err)
		}
	}
	if fallbackBinaryUpdate != nil {
		if err := fallbackBinaryUpdate.commit(); err != nil {
			c.logger.Printf("remove previous fallback miner binary: %v", err)
		}
	}
	c.configuration = configuration
	c.failover.reset()
	c.watchdogStopped = false
	c.appliedRevision.Store(configuration.Revision)
	return nil
}

func (c *Client) startResolvedWorkload(configuration protocol.ResolvedConfiguration) error {
	return c.startCPUWorkload(protocol.ResolvedWorkload{
		Miner: configuration.Miner, Wallet: configuration.Wallet, Pool: configuration.Pool,
		MiningUser: configuration.MiningUser, WorkerName: configuration.WorkerName,
		Algorithm: configuration.Miner.Algorithm, Coin: configuration.Wallet.Coin,
	}, c.config.Name, configuration.GPUPowerLimitW)
}

func (c *Client) startCPUWorkload(workload protocol.ResolvedWorkload, workerName string, gpuPowerLimitW int) error {
	if gpuPowerLimitW > 0 {
		gpus := c.metrics.Collect(c.miner.State()).GPUs
		if len(gpus) > 0 {
			if err := enforceGPUPowerLimit(gpuPowerLimitW, gpus); err != nil {
				if stopErr := c.miner.Stop(); stopErr != nil {
					return fmt.Errorf("enforce controller GPU power policy: %v; stop miner: %w", err, stopErr)
				}
				return fmt.Errorf("enforce controller GPU power policy: %w", err)
			}
			c.lastPowerLimitEnforced = time.Now()
		}
	}
	if err := c.miner.Stop(); err != nil {
		return err
	}
	if len(c.metrics.Collect(c.miner.State()).GPUs) == 0 {
		if err := c.cpuOptimizer.prepare(workload.Algorithm); err != nil {
			c.logger.Printf("prepare CPU optimization for %s: %v", workload.Algorithm, err)
		}
	}
	return c.miner.StartWorkload(workload, workerName)
}

func (c *Client) ensureManagedMiner(ctx context.Context, configuration *protocol.ResolvedConfiguration) (*minerBinaryUpdate, error) {
	miner := &configuration.Miner
	if !miner.ManagedBinary {
		if miner.CatalogID == "xmrig" {
			path, err := exec.LookPath("xmrig")
			if err != nil {
				return nil, errors.New("the Rig OS XMRig package is not installed")
			}
			apiTokenDigest := sha256.Sum256([]byte("minerdash-xmrig-api:" + c.identity.Token))
			apiToken := hex.EncodeToString(apiTokenDigest[:])
			c.miner.SetProfile("xmrig", Profile{
				Command: path,
				Args: append(
					append([]string(nil), miner.DefaultArguments...),
					"--http-host", "127.0.0.1", "--http-port", "18080",
					"--http-access-token", apiToken, "--http-no-restricted",
				),
				StatsType: "xmrig", StatsURL: "http://127.0.0.1:18080/2/summary", StatsToken: apiToken,
			})
			miner.Profile = "xmrig"
		}
		return nil, nil
	}
	if miner.PackageFormat == "tar.gz" {
		if miner.ID == "" || !validManagedBundlePath(miner.EntryPoint) || len(miner.PackageSHA256) != 64 || len(miner.BinarySHA256) != 64 {
			return nil, errors.New("managed miner bundle metadata is incomplete")
		}
	} else if miner.ID == "" || miner.BinaryName == "" || filepath.Base(miner.BinaryName) != miner.BinaryName || len(miner.BinarySHA256) != 64 {
		return nil, errors.New("managed miner metadata is incomplete")
	}
	directory := filepath.Join(c.config.PackageDir, miner.ID)
	var update *minerBinaryUpdate
	var err error
	var executablePath string
	if miner.PackageFormat == "tar.gz" {
		executablePath = filepath.Join(directory, filepath.FromSlash(miner.EntryPoint))
		digest, digestErr := fileSHA256(executablePath)
		if digestErr != nil && !errors.Is(digestErr, os.ErrNotExist) {
			return nil, digestErr
		}
		if digest != strings.ToLower(miner.BinarySHA256) {
			update, err = c.downloadMinerBundle(ctx, miner.ID, directory, miner.EntryPoint, strings.ToLower(miner.PackageSHA256), strings.ToLower(miner.BinarySHA256))
			if err != nil {
				return nil, err
			}
		}
	} else {
		executablePath = filepath.Join(directory, miner.BinaryName)
		digest, digestErr := fileSHA256(executablePath)
		if digestErr != nil && !errors.Is(digestErr, os.ErrNotExist) {
			return nil, digestErr
		}
		if digest != strings.ToLower(miner.BinarySHA256) {
			update, err = c.downloadMiner(ctx, miner.ID, executablePath, strings.ToLower(miner.BinarySHA256))
			if err != nil {
				return nil, err
			}
		}
	}
	profileName := "managed-" + miner.ID
	workingDirectory := directory
	if miner.PackageFormat == "tar.gz" {
		workingDirectory = filepath.Dir(executablePath)
	}
	profile := Profile{
		Command: executablePath, Args: miner.DefaultArguments, WorkingDir: workingDirectory,
		Env: miner.Environment, StatsType: miner.StatsType, StatsURL: miner.StatsURL,
	}
	if strings.EqualFold(miner.BinaryName, "miniZ") && profile.StatsType == "" {
		profile.StatsType = "miniz"
		profile.StatsURL = "http://127.0.0.1:20000/getstat"
	}
	if strings.EqualFold(miner.BinaryName, "SRBMiner-MULTI") && profile.StatsType == "" {
		profile.StatsType = "srbminer"
		profile.StatsURL = "http://127.0.0.1:21550/"
	}
	if strings.EqualFold(miner.BinaryName, "epic-miner-launch") && profile.StatsType == "" {
		profile.StatsType = "epic"
	}
	if strings.EqualFold(miner.BinaryName, "t-rex") {
		if profile.StatsType == "" {
			profile.StatsType = "trex"
			profile.StatsURL = "http://127.0.0.1:4067/summary"
		}
		profile.Args = ensureTRexAPIArguments(profile.Args)
	}
	c.miner.SetProfile(profileName, profile)
	miner.Profile = profileName
	return update, nil
}

// ensureTRexAPIArguments guarantees the T-Rex HTTP API is bound to loopback so
// hashrate telemetry can be read, without overriding an explicit operator bind.
func ensureTRexAPIArguments(args []string) []string {
	for _, argument := range args {
		if strings.HasPrefix(argument, "--api-bind-http") {
			return args
		}
	}
	return append(append([]string(nil), args...), "--api-bind-http", "127.0.0.1:4067")
}

func (c *Client) downloadMinerBundle(ctx context.Context, minerID, destination, entryPoint, packageHash, executableHash string) (*minerBinaryUpdate, error) {
	archivePath := filepath.Join(c.config.PackageDir, minerID+".download.tar.gz")
	if err := c.downloadMinerPayload(ctx, minerID, archivePath, packageHash, maxManagedMinerPackageSize); err != nil {
		return nil, err
	}
	defer os.Remove(archivePath)
	staged := destination + ".next"
	if err := os.RemoveAll(staged); err != nil {
		return nil, err
	}
	if err := extractManagedMinerBundle(archivePath, staged); err != nil {
		_ = os.RemoveAll(staged)
		return nil, err
	}
	executable := filepath.Join(staged, filepath.FromSlash(entryPoint))
	digest, err := fileSHA256(executable)
	if err != nil {
		_ = os.RemoveAll(staged)
		return nil, fmt.Errorf("verify bundle entry point: %w", err)
	}
	if digest != executableHash {
		_ = os.RemoveAll(staged)
		return nil, errors.New("managed miner bundle entry point SHA-256 mismatch")
	}
	if err := os.Chmod(executable, 0o700); err != nil {
		_ = os.RemoveAll(staged)
		return nil, err
	}
	return replaceManagedMinerPath(staged, destination, true)
}

func extractManagedMinerBundle(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open managed miner bundle: %w", err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	var total int64
	var files int
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read managed miner bundle: %w", nextErr)
		}
		archiveName := header.Name
		if header.Typeflag == tar.TypeDir {
			archiveName = strings.TrimSuffix(archiveName, "/")
		}
		clean := path.Clean(strings.ReplaceAll(archiveName, "\\", "/"))
		if !validManagedBundlePath(clean) || clean != archiveName {
			return fmt.Errorf("managed miner bundle contains unsafe path %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			files++
			total += header.Size
			if files > 10000 || header.Size < 0 || header.Size > maxManagedMinerSize || total > maxManagedMinerPackageSize {
				return errors.New("managed miner bundle expands beyond safety limits")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o700)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, archive)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("managed miner bundle contains unsupported entry %q", header.Name)
		}
	}
	return nil
}

func validManagedBundlePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func (c *Client) downloadMinerPayload(ctx context.Context, minerID, destination, expectedHash string, maximumSize int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.config.Controller, "/")+"/api/v1/agents/"+c.identity.AgentID+"/miners/"+minerID+"/binary", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.identity.Token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("miner download returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maximumSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	if written > maximumSize {
		_ = os.Remove(destination)
		return fmt.Errorf("managed miner package exceeds %d bytes", maximumSize)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedHash {
		_ = os.Remove(destination)
		return fmt.Errorf("managed miner package SHA-256 mismatch: got %s", actual)
	}
	return nil
}

func (c *Client) downloadMiner(ctx context.Context, minerID, destination, expectedHash string) (*minerBinaryUpdate, error) {
	temp := destination + ".download"
	if err := c.downloadMinerPayload(ctx, minerID, temp, expectedHash, maxManagedMinerSize); err != nil {
		return nil, err
	}
	if err := os.Chmod(temp, 0o700); err != nil {
		_ = os.Remove(temp)
		return nil, err
	}
	return replaceManagedMinerBinary(temp, destination)
}

type minerBinaryUpdate struct {
	destination string
	backup      string
	hadPrevious bool
	directory   bool
}

func replaceManagedMinerBinary(staged, destination string) (*minerBinaryUpdate, error) {
	update := &minerBinaryUpdate{destination: destination, backup: destination + ".previous"}
	if err := os.Remove(update.backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(destination); err == nil {
		if err := os.Rename(destination, update.backup); err != nil {
			return nil, err
		}
		update.hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(staged, destination); err != nil {
		if update.hadPrevious {
			_ = os.Rename(update.backup, destination)
		}
		return nil, err
	}
	return update, nil
}

func replaceManagedMinerPath(staged, destination string, directory bool) (*minerBinaryUpdate, error) {
	update := &minerBinaryUpdate{destination: destination, backup: destination + ".previous", directory: directory}
	remove := os.Remove
	if directory {
		remove = os.RemoveAll
	}
	if err := remove(update.backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(destination); err == nil {
		if err := os.Rename(destination, update.backup); err != nil {
			return nil, err
		}
		update.hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(staged, destination); err != nil {
		if update.hadPrevious {
			_ = os.Rename(update.backup, destination)
		}
		return nil, err
	}
	return update, nil
}

func (u *minerBinaryUpdate) rollback() error {
	remove := os.Remove
	if u.directory {
		remove = os.RemoveAll
	}
	if err := remove(u.destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if u.hadPrevious {
		return os.Rename(u.backup, u.destination)
	}
	return nil
}

func (u *minerBinaryUpdate) commit() error {
	if !u.hadPrevious {
		return nil
	}
	if u.directory {
		return os.RemoveAll(u.backup)
	}
	return os.Remove(u.backup)
}

const maxManagedMinerSize = 250 << 20
const maxManagedMinerPackageSize = 1 << 30

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *Client) enforceWatchdog(metrics protocol.Metrics) error {
	policy := c.configuration.Watchdog
	if !policy.Enabled || policy.MaxTemperatureC <= 0 {
		return nil
	}
	tooHot := false
	for _, gpu := range metrics.GPUs {
		if gpu.Temperature >= policy.MaxTemperatureC {
			tooHot = true
			break
		}
	}
	if tooHot && !c.watchdogStopped {
		if err := c.miner.Stop(); err != nil {
			return err
		}
		c.watchdogStopped = true
		return nil
	}
	if !tooHot && c.watchdogStopped {
		if err := c.startActiveWorkload(); err != nil {
			return err
		}
		c.watchdogStopped = false
	}
	// Smart Tune deliberately lowers power limits and hashrate while it measures
	// each GPU, so the minimum-hashrate watchdog must not restart the miner and
	// invalidate the run that is in progress.
	if c.watchdogStopped || policy.MinHashrate <= 0 || !metrics.Miner.Running || c.tuner.active() {
		c.lowHashSince = time.Time{}
		return nil
	}
	if metrics.Miner.Hashrate >= policy.MinHashrate {
		c.lowHashSince = time.Time{}
		c.watchdogFailures = 0
		return nil
	}
	if c.lowHashSince.IsZero() {
		c.lowHashSince = time.Now()
		return nil
	}
	delay := time.Duration(policy.RestartAfterSeconds) * time.Second
	if delay <= 0 {
		delay = 60 * time.Second
	}
	if time.Since(c.lowHashSince) < delay {
		return nil
	}
	c.lowHashSince = time.Now()
	c.watchdogFailures++
	if policy.RebootAfterFailures > 0 && c.watchdogFailures >= policy.RebootAfterFailures {
		_, err := executeSystemAction("reboot")
		return err
	}
	return c.startActiveWorkload()
}

func validateResolvedConfiguration(configuration protocol.ResolvedConfiguration) error {
	if configuration.Miner.Profile == "" {
		return nil
	}
	if configuration.Pool.URL == "" || configuration.Wallet.Address == "" {
		return errors.New("assigned miner requires a pool URL and wallet address")
	}
	if configuration.Fallback != nil &&
		(configuration.Fallback.Miner.Profile == "" && !configuration.Fallback.Miner.ManagedBinary ||
			configuration.Fallback.Pool.URL == "" || configuration.Fallback.Wallet.Address == "") {
		return errors.New("fallback miner requires a profile, pool URL, and wallet address")
	}
	return nil
}

func (c *Client) pollCommand(ctx context.Context) error {
	if c.pendingResult != nil {
		if err := c.submitCommandResult(ctx, c.pendingResult.commandID, c.pendingResult.result); err != nil {
			return err
		}
		c.pendingResult = nil
	}
	var command protocol.Command
	err := c.request(ctx, http.MethodGet, "/api/v1/agents/"+c.identity.AgentID+"/commands/next", c.identity.Token, nil, &command)
	if errors.Is(err, errNoContent) {
		return nil
	}
	if err != nil {
		return err
	}
	if command.Action == "smart-tune" {
		err := c.tuner.start(ctx, command, c.configuration.GPUPowerLimitW, c.miner, c.logger, func(result protocol.CommandResult) {
			c.finishSmartTune(command.ID, result)
		})
		if err == nil {
			return nil
		}
		result := protocol.CommandResult{Error: err.Error()}
		return c.submitCommandResult(ctx, command.ID, result)
	}
	if command.Action == "reboot" || command.Action == "shutdown" {
		if err := c.submitCommandResult(ctx, command.ID, protocol.CommandResult{Output: "system action accepted"}); err != nil {
			return err
		}
		_, returnErr := c.executeCommand(ctx, command)
		return returnErr
	}
	output, executeErr := c.executeCommand(ctx, command)
	result := protocol.CommandResult{}
	if executeErr != nil {
		result.Error = executeErr.Error()
	}
	result.Output = output
	c.pendingResult = &pendingCommandResult{commandID: command.ID, result: result}
	if err := c.submitCommandResult(ctx, command.ID, result); err != nil {
		return err
	}
	c.pendingResult = nil
	return nil
}

func (c *Client) submitCommandResult(ctx context.Context, commandID string, result protocol.CommandResult) error {
	err := c.request(ctx, http.MethodPost, "/api/v1/agents/"+c.identity.AgentID+"/commands/"+commandID+"/result", c.identity.Token, result, nil)
	if errors.Is(err, errNoContent) {
		return nil
	}
	return err
}

func (c *Client) executeCommand(ctx context.Context, command protocol.Command) (string, error) {
	switch command.Action {
	case "shell-command":
		return executeShellCommand(command.ShellCommand)
	case "start", "restart":
		if err := c.syncAndRecordConfiguration(ctx); err != nil {
			return "", fmt.Errorf("load assigned flight sheet: %w", err)
		}
		if err := c.startActiveWorkload(); err != nil {
			return "", fmt.Errorf("start assigned miner: %w", err)
		}
		return "assigned flight-sheet miner started", nil
	case "stop":
		return "", c.miner.Execute(command.Action, command.Profile)
	case "logs":
		return c.miner.Logs(), nil
	case "stop-smart-tune":
		if c.tuner.stop() {
			return "Smart tune cancellation requested; original power limits will be restored.", nil
		}
		return "Smart tune is not running.", nil
	case "gpu-driver-update":
		if command.DriverUpdate == nil {
			return "", errors.New("GPU driver update settings are required")
		}
		if err := validateDriverSelection(*command.DriverUpdate); err != nil {
			return "", err
		}
		if c.miner.State().Running {
			if err := c.miner.Execute("stop", ""); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return "", fmt.Errorf("stop miner before driver update: %w", err)
			}
		}
		return executeGPUDriverUpdate(*command.DriverUpdate)
	case "gpu-driver-list":
		if command.DriverUpdate == nil {
			return "", errors.New("GPU driver vendor settings are required")
		}
		return listGPUDriverPackages(*command.DriverUpdate)
	case "reboot", "shutdown":
		return executeSystemAction(command.Action)
	default:
		return "", fmt.Errorf("unsupported command action %q", command.Action)
	}
}

func (c *Client) finishSmartTune(commandID string, result protocol.CommandResult) {
	for attempt := 0; attempt < 5; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := c.submitCommandResult(ctx, commandID, result)
		cancel()
		if err == nil {
			return
		}
		c.logger.Printf("submit smart tune result failed: %v", err)
		time.Sleep(5 * time.Second)
	}
}

var errNoContent = errors.New("no content")

func (c *Client) request(ctx context.Context, method, path, token string, body, response any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.config.Controller, "/")+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	result, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	if result.StatusCode == http.StatusNoContent {
		return errNoContent
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(result.Body, 4096))
		return fmt.Errorf("controller returned %s: %s", result.Status, strings.TrimSpace(string(message)))
	}
	if response != nil {
		return json.NewDecoder(io.LimitReader(result.Body, 1<<20)).Decode(response)
	}
	return nil
}

func normalizeFingerprint(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), ":", ""))
}
