package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"minerdash/internal/protocol"
)

type MinerManager struct {
	mu        sync.Mutex
	profiles  map[string]Profile
	command   *exec.Cmd
	done      chan struct{}
	profile   string
	lastError string
	logs      *boundedBuffer
}

func NewMinerManager(profiles map[string]Profile) *MinerManager {
	return &MinerManager{profiles: profiles, logs: &boundedBuffer{limit: 64 * 1024}}
}

func (m *MinerManager) SetProfile(name string, profile Profile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles[name] = profile
}

func (m *MinerManager) Execute(action, profile string) error {
	switch action {
	case "start":
		if profile == "" {
			m.mu.Lock()
			profile = m.profile
			m.mu.Unlock()
		}
		if profile == "" {
			return errors.New("no miner profile is selected")
		}
		return m.Start(profile)
	case "stop":
		return m.Stop()
	case "restart":
		m.mu.Lock()
		current := m.profile
		m.mu.Unlock()
		if profile == "" {
			profile = current
		}
		if profile == "" {
			return errors.New("no miner profile is selected")
		}
		if err := m.Stop(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return m.Start(profile)
	default:
		return fmt.Errorf("unsupported action %q", action)
	}
}

func (m *MinerManager) Start(name string) error {
	return m.start(name, nil, nil)
}

func (m *MinerManager) StartResolved(configuration protocol.ResolvedConfiguration, workerName string) error {
	if configuration.Miner.Profile == "" {
		return m.Stop()
	}
	if configuration.WorkerName != "" {
		workerName = configuration.WorkerName
	}
	wallet := configuration.Wallet.Address
	if configuration.MiningUser != "" {
		wallet = configuration.MiningUser
	}
	replacements := map[string]string{
		"{POOL}":          configuration.Pool.URL,
		"{POOL_ENDPOINT}": poolEndpoint(configuration.Pool.URL),
		"{WALLET}":        wallet,
		"{PASSWORD}":      configuration.Pool.Password,
		"{WORKER}":        workerName,
		"{COIN}":          configuration.Wallet.Coin,
		"{ALGORITHM}":     configuration.Miner.Algorithm,
	}
	if err := m.Stop(); err != nil {
		return err
	}
	return m.start(configuration.Miner.Profile, configuration.Miner.ExtraArguments, replacements)
}

func resolveArguments(arguments []string, replacements map[string]string) []string {
	result := make([]string, len(arguments))
	pairs := make([]string, 0, len(replacements)*2)
	for placeholder, value := range replacements {
		pairs = append(pairs, placeholder, value)
	}
	replacer := strings.NewReplacer(pairs...)
	for index, argument := range arguments {
		result[index] = replacer.Replace(argument)
	}
	return result
}

func (m *MinerManager) start(name string, extraArguments []string, replacements map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command != nil && m.command.ProcessState == nil {
		return errors.New("a miner is already running")
	}
	profile, ok := m.profiles[name]
	if !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	arguments := resolvedCommandArguments(profile, extraArguments, replacements)
	command := exec.Command(profile.Command, arguments...)
	command.Dir = profile.WorkingDir
	command.Env = append(os.Environ(), envList(profile.Env)...)
	command.Stdout = io.MultiWriter(os.Stdout, m.logs)
	command.Stderr = io.MultiWriter(os.Stderr, m.logs)
	if err := command.Start(); err != nil {
		m.lastError = err.Error()
		return err
	}
	m.command = command
	m.done = make(chan struct{})
	m.profile = name
	m.lastError = ""
	go m.wait(command, m.done)
	return nil
}

func resolvedCommandArguments(profile Profile, extraArguments []string, replacements map[string]string) []string {
	return append(resolveArguments(profile.Args, replacements), resolveArguments(extraArguments, replacements)...)
}

func (m *MinerManager) Logs() string {
	return m.logs.String()
}

func (m *MinerManager) Stop() error {
	m.mu.Lock()
	if m.command == nil || m.command.ProcessState != nil {
		m.mu.Unlock()
		return nil
	}
	command := m.command
	done := m.done
	m.mu.Unlock()
	if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("timed out waiting for miner process to stop")
	}
}

func (m *MinerManager) State() protocol.MinerState {
	m.mu.Lock()
	state := protocol.MinerState{Profile: m.profile, LastError: m.lastError}
	profile := m.profiles[m.profile]
	if m.command != nil && m.command.ProcessState == nil {
		state.Running = true
		state.PID = m.command.Process.Pid
	}
	m.mu.Unlock()
	if state.Running && profile.StatsType == "xmrig" {
		readXMRigStats(profile.StatsURL, profile.StatsToken, &state)
	}
	if state.Running && profile.StatsType == "miniz" {
		readMiniZStats(profile.StatsURL, &state)
	}
	if state.Running && profile.StatsType == "srbminer" {
		readSRBMinerStats(profile.StatsURL, &state)
	}
	if state.Running && profile.StatsType == "command" {
		readCommandStats(profile, &state)
	}
	return state
}

func readSRBMinerStats(endpoint string, state *protocol.MinerState) {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return
	}
	var report struct {
		HashrateTotalNow float64 `json:"hashrate_total_now"`
		Shares           struct {
			Accepted uint64 `json:"accepted"`
			Rejected uint64 `json:"rejected"`
		} `json:"shares"`
		Algorithms []struct {
			Algorithm string `json:"algorithm"`
			Hashrate  struct {
				CPU map[string]float64 `json:"cpu"`
				GPU map[string]float64 `json:"gpu"`
			} `json:"hashrate"`
			Shares struct {
				Accepted uint64 `json:"accepted"`
				Rejected uint64 `json:"rejected"`
			} `json:"shares"`
		} `json:"algorithms"`
		Devices []struct {
			ID               int     `json:"id"`
			Device           int     `json:"device"`
			Name             string  `json:"name"`
			HashrateTotalNow float64 `json:"hashrate_total_now"`
			HashrateNow      float64 `json:"hashrate_now"`
		} `json:"devices"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&report) != nil {
		return
	}
	state.Hashrate = report.HashrateTotalNow
	state.HashrateUnit = "H/s"
	state.AcceptedShares = report.Shares.Accepted
	state.RejectedShares = report.Shares.Rejected
	state.Devices = state.Devices[:0]
	deviceIndex := 0
	for _, algorithm := range report.Algorithms {
		state.Hashrate += algorithm.Hashrate.CPU["total"] + algorithm.Hashrate.GPU["total"]
		state.AcceptedShares += algorithm.Shares.Accepted
		state.RejectedShares += algorithm.Shares.Rejected
		for thread := 0; ; thread++ {
			hashrate, ok := algorithm.Hashrate.CPU[fmt.Sprintf("thread%d", thread)]
			if !ok {
				break
			}
			state.Devices = append(state.Devices, protocol.DeviceHashrate{
				Index: deviceIndex, Kind: "CPU thread", Name: fmt.Sprintf("Thread %d", thread),
				Hashrate: hashrate, Unit: "H/s",
			})
			deviceIndex++
		}
	}
	for index, device := range report.Devices {
		hashrate := device.HashrateTotalNow
		if hashrate == 0 {
			hashrate = device.HashrateNow
		}
		if hashrate <= 0 {
			continue
		}
		deviceIndex := device.ID
		if deviceIndex == 0 && device.Device != 0 {
			deviceIndex = device.Device
		}
		name := device.Name
		if name == "" {
			name = fmt.Sprintf("Device %d", deviceIndex)
		}
		state.Devices = append(state.Devices, protocol.DeviceHashrate{
			Index: deviceIndex, Kind: "CPU", Name: name, Hashrate: hashrate, Unit: "H/s",
		})
		if deviceIndex == 0 && device.ID == 0 && device.Device == 0 && index > 0 {
			state.Devices[len(state.Devices)-1].Index = index
		}
	}
	if state.Hashrate == 0 {
		for _, device := range state.Devices {
			state.Hashrate += device.Hashrate
		}
	}
}

func poolEndpoint(poolURL string) string {
	if _, endpoint, found := strings.Cut(poolURL, "://"); found {
		return endpoint
	}
	return poolURL
}

func readMiniZStats(endpoint string, state *protocol.MinerState) {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return
	}
	var report struct {
		Result []struct {
			GPUID          int     `json:"gpuid"`
			Name           string  `json:"name"`
			Speed          float64 `json:"speed_sps"`
			AcceptedShares uint64  `json:"accepted_shares"`
			RejectedShares uint64  `json:"rejected_shares"`
		} `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&report) != nil {
		return
	}
	state.HashrateUnit = "Sol/s"
	state.Devices = state.Devices[:0]
	for _, gpu := range report.Result {
		state.Hashrate += gpu.Speed
		state.AcceptedShares += gpu.AcceptedShares
		state.RejectedShares += gpu.RejectedShares
		state.Devices = append(state.Devices, protocol.DeviceHashrate{
			Index: gpu.GPUID, Kind: "GPU", Name: gpu.Name, Hashrate: gpu.Speed, Unit: "Sol/s",
		})
	}
}

func readCommandStats(profile Profile, state *protocol.MinerState) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, profile.StatsCommand, profile.StatsArgs...).Output()
	if err != nil || len(output) > 1<<20 {
		return
	}
	var reported protocol.MinerState
	if json.Unmarshal(output, &reported) != nil {
		return
	}
	state.Hashrate = reported.Hashrate
	state.HashrateUnit = reported.HashrateUnit
	state.AcceptedShares = reported.AcceptedShares
	state.RejectedShares = reported.RejectedShares
	state.Devices = reported.Devices
}

func readXMRigStats(endpoint, token string, state *protocol.MinerState) {
	client := &http.Client{Timeout: 2 * time.Second}
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return
	}
	var summary struct {
		Hashrate struct {
			Total   []float64   `json:"total"`
			Threads [][]float64 `json:"threads"`
		} `json:"hashrate"`
		Connection struct {
			Accepted uint64 `json:"accepted"`
			Rejected uint64 `json:"rejected"`
		} `json:"connection"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&summary) != nil || len(summary.Hashrate.Total) == 0 {
		return
	}
	state.Hashrate = summary.Hashrate.Total[0]
	state.HashrateUnit = "H/s"
	state.AcceptedShares = summary.Connection.Accepted
	state.RejectedShares = summary.Connection.Rejected
	state.Devices = state.Devices[:0]
	for index, values := range summary.Hashrate.Threads {
		if len(values) == 0 {
			continue
		}
		state.Devices = append(state.Devices, protocol.DeviceHashrate{
			Index: index, Kind: "CPU thread", Name: fmt.Sprintf("Thread %d", index),
			Hashrate: values[0], Unit: "H/s",
		})
	}
	if len(state.Devices) == 0 {
		readXMRigBackendThreads(client, strings.TrimSuffix(endpoint, "/summary")+"/backends", token, state)
	}
}

func readXMRigBackendThreads(client *http.Client, endpoint, token string, state *protocol.MinerState) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return
	}
	var backends []struct {
		Type    string `json:"type"`
		Threads []struct {
			Hashrate []float64 `json:"hashrate"`
		} `json:"threads"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&backends) != nil {
		return
	}
	index := 0
	for _, backend := range backends {
		if backend.Type != "cpu" {
			continue
		}
		for _, thread := range backend.Threads {
			if len(thread.Hashrate) == 0 {
				continue
			}
			state.Devices = append(state.Devices, protocol.DeviceHashrate{
				Index: index, Kind: "CPU thread", Name: fmt.Sprintf("Thread %d", index),
				Hashrate: thread.Hashrate[0], Unit: "H/s",
			})
			index++
		}
	}
}

func (m *MinerManager) wait(command *exec.Cmd, done chan struct{}) {
	err := command.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(done)
	if m.command != command {
		return
	}
	if err != nil && !strings.Contains(err.Error(), "signal: killed") {
		m.lastError = err.Error()
	}
}

func envList(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

type boundedBuffer struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(value)
	if len(value) >= b.limit {
		b.data.Reset()
		_, _ = b.data.Write(value[len(value)-b.limit:])
		return written, nil
	}
	overflow := b.data.Len() + len(value) - b.limit
	if overflow > 0 {
		remaining := append([]byte(nil), b.data.Bytes()[overflow:]...)
		b.data.Reset()
		_, _ = b.data.Write(remaining)
	}
	_, _ = b.data.Write(value)
	return written, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}
