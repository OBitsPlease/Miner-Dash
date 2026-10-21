package agent

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

const (
	defaultEpicFailoverDelay = 2 * time.Second
	defaultEpicReturnDelay   = 2 * time.Second
	epicProbeFailureDelay    = 60 * time.Second
)

type epicFailoverState struct {
	activeFallback    bool
	unavailableSince  time.Time
	availableSince    time.Time
	state             string
	reason            string
	observedAlgorithm string
	height            uint64
}

type epicStratumStatus struct {
	Algorithm string
	Height    uint64
	Reason    string
}

func (s *epicFailoverState) reset() {
	*s = epicFailoverState{}
}

func (c *Client) reconcileEpicFailover(ctx context.Context) error {
	configuration := c.configuration
	if configuration.Miner.CatalogID != "epic-miner" || configuration.Fallback == nil {
		c.failover.reset()
		return nil
	}

	status, probeErr := probeEpicStratum(ctx, configuration.Pool.URL)
	now := time.Now()
	c.failover.observedAlgorithm = strings.ToLower(status.Algorithm)
	c.failover.height = status.Height
	primaryAvailable := probeErr == nil && strings.EqualFold(status.Algorithm, configuration.Miner.Algorithm)
	if epicPrimaryHashing(c.failover.activeFallback, c.miner.State(), probeErr) {
		primaryAvailable = true
		status.Algorithm = configuration.Miner.Algorithm
	}

	if primaryAvailable {
		c.failover.unavailableSince = time.Time{}
		if !c.failover.activeFallback {
			c.failover.availableSince = time.Time{}
			c.failover.state = "primary"
			c.failover.reason = fmt.Sprintf("Epic %s is active", status.Algorithm)
			return nil
		}
		if c.failover.availableSince.IsZero() {
			c.failover.availableSince = now
		}
		c.failover.state = "returning"
		c.failover.reason = fmt.Sprintf("Epic %s returned; waiting for a stable job", status.Algorithm)
		if now.Sub(c.failover.availableSince) < returnDelay(configuration.ReturnAfterSeconds) || c.watchdogStopped {
			return nil
		}
		if err := c.startResolvedWorkload(configuration); err != nil {
			return fmt.Errorf("return to Epic primary workload: %w", err)
		}
		c.failover.activeFallback = false
		c.failover.availableSince = time.Time{}
		c.failover.state = "primary"
		c.failover.reason = fmt.Sprintf("Epic %s is active", status.Algorithm)
		c.lowHashSince = time.Time{}
		c.watchdogFailures = 0
		return nil
	}

	c.failover.availableSince = time.Time{}
	if c.failover.unavailableSince.IsZero() {
		c.failover.unavailableSince = now
	}
	reason := status.Reason
	if probeErr != nil {
		reason = probeErr.Error()
	} else if reason == "" && status.Algorithm != "" {
		reason = fmt.Sprintf("Epic currently requires %s", status.Algorithm)
	} else if reason == "" {
		reason = "Epic Stratum is not ready"
	}
	if c.failover.activeFallback {
		c.failover.state = "fallback"
		c.failover.reason = reason
		return nil
	}
	c.failover.state = "waiting"
	c.failover.reason = reason
	delay := epicFailoverWait(configuration.FailoverAfterSeconds, probeErr)
	if now.Sub(c.failover.unavailableSince) < delay || c.watchdogStopped {
		return nil
	}
	if err := c.startCPUWorkload(*configuration.Fallback, c.config.Name, configuration.GPUPowerLimitW); err != nil {
		return fmt.Errorf("start fallback workload: %w", err)
	}
	c.failover.activeFallback = true
	c.failover.state = "fallback"
	c.lowHashSince = time.Time{}
	c.watchdogFailures = 0
	return nil
}

func (c *Client) startActiveWorkload() error {
	if c.failover.activeFallback && c.configuration.Fallback != nil {
		return c.startCPUWorkload(*c.configuration.Fallback, c.config.Name, c.configuration.GPUPowerLimitW)
	}
	return c.startResolvedWorkload(c.configuration)
}

func (c *Client) applyMiningStatus(metrics *protocol.Metrics) {
	metrics.PrimaryMiningCoin = c.configuration.Wallet.Coin
	metrics.PrimaryMiningAlgorithm = c.configuration.Miner.Algorithm
	metrics.MiningCoin = c.configuration.Wallet.Coin
	metrics.MiningAlgorithm = c.configuration.Miner.Algorithm
	if c.failover.activeFallback && c.configuration.Fallback != nil {
		metrics.MiningCoin = c.configuration.Fallback.Coin
		metrics.MiningAlgorithm = c.configuration.Fallback.Algorithm
	}
	metrics.MiningState = c.failover.state
	metrics.MiningReason = c.failover.reason
	metrics.ObservedMiningAlgorithm = c.failover.observedAlgorithm
	metrics.MiningHeight = c.failover.height
}

func failoverDelay(seconds int) time.Duration {
	delay := defaultEpicFailoverDelay
	if seconds > 0 {
		delay = time.Duration(seconds) * time.Second
	}
	return min(delay, defaultEpicFailoverDelay)
}

func returnDelay(seconds int) time.Duration {
	delay := defaultEpicReturnDelay
	if seconds > 0 {
		delay = time.Duration(seconds) * time.Second
	}
	return min(delay, defaultEpicReturnDelay)
}

func epicFailoverWait(seconds int, probeErr error) time.Duration {
	delay := failoverDelay(seconds)
	if probeErr != nil && delay < epicProbeFailureDelay {
		return epicProbeFailureDelay
	}
	return delay
}

func epicPrimaryHashing(activeFallback bool, miner protocol.MinerState, probeErr error) bool {
	return !activeFallback && probeErr != nil && miner.Running && miner.Hashrate > 0
}

func probeEpicStratum(ctx context.Context, rawURL string) (epicStratumStatus, error) {
	status, err := probeEpicCachedStatus(ctx, rawURL)
	if err == nil && status.Reason == "" {
		return status, nil
	}
	return probeEpicJobTemplate(ctx, rawURL)
}

func probeEpicCachedStatus(ctx context.Context, rawURL string) (epicStratumStatus, error) {
	return probeEpicMethod(ctx, rawURL, "minerdash-status", "minerdash_status", nil)
}

func probeEpicJobTemplate(ctx context.Context, rawURL string) (epicStratumStatus, error) {
	return probeEpicMethod(ctx, rawURL, "minerdash-status", "getjobtemplate", map[string]any{"algorithm": "randomx"})
}

func probeEpicMethod(ctx context.Context, rawURL, id, method string, params map[string]any) (epicStratumStatus, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return epicStratumStatus{}, errors.New("invalid Epic Stratum URL")
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	var connection net.Conn
	switch parsed.Scheme {
	case "stratum+tcp":
		connection, err = dialer.DialContext(ctx, "tcp", parsed.Host)
	case "stratum+ssl", "stratum+tls", "stratum+tcps":
		connection, err = (&tls.Dialer{
			NetDialer: dialer,
			Config: &tls.Config{
				MinVersion: tls.VersionTLS12,
				ServerName: parsed.Hostname(),
			},
		}).DialContext(ctx, "tcp", parsed.Host)
	default:
		return epicStratumStatus{}, fmt.Errorf("unsupported Epic Stratum scheme %q", parsed.Scheme)
	}
	if err != nil {
		return epicStratumStatus{}, fmt.Errorf("connect to Epic Stratum: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return epicStratumStatus{}, err
	}

	request := struct {
		ID      string         `json:"id"`
		JSONRPC string         `json:"jsonrpc"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
	}{
		ID: id, JSONRPC: "2.0", Method: method, Params: params,
	}
	if method == "getjobtemplate" {
		login := struct {
			ID      string         `json:"id"`
			JSONRPC string         `json:"jsonrpc"`
			Method  string         `json:"method"`
			Params  map[string]any `json:"params"`
		}{
			ID: "minerdash-login", JSONRPC: "2.0", Method: "login",
			Params: map[string]any{"login": "minerdash", "pass": "x", "agent": "minerdash-readiness-probe"},
		}
		if err := json.NewEncoder(connection).Encode(login); err != nil {
			return epicStratumStatus{}, fmt.Errorf("log in to Epic Stratum: %w", err)
		}
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return epicStratumStatus{}, fmt.Errorf("request Epic job: %w", err)
	}

	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	response, err := readEpicStratumResponse(scanner, request.ID, request.Method)
	if err != nil {
		return epicStratumStatus{}, fmt.Errorf("read Epic job: %w", err)
	}
	if response.Error != "" {
		return epicStratumStatus{Reason: response.Error}, nil
	}
	if response.Algorithm == "" {
		return epicStratumStatus{}, errors.New("Epic Stratum returned an incomplete job template")
	}
	return epicStratumStatus{Algorithm: response.Algorithm, Height: response.Height}, nil
}

type epicStratumResponse struct {
	Algorithm string
	Height    uint64
	Error     string
}

func readEpicStratumResponse(scanner *bufio.Scanner, id, method string) (epicStratumResponse, error) {
	for scanner.Scan() {
		var response struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			continue
		}
		if response.ID != id || (response.Method != "" && response.Method != method) {
			continue
		}
		if response.Error != nil {
			return epicStratumResponse{Error: response.Error.Message}, nil
		}
		if len(response.Result) == 0 || string(response.Result) == "null" {
			return epicStratumResponse{}, errors.New("Epic Stratum returned an incomplete response")
		}
		var result struct {
			Algorithm string `json:"algorithm"`
			Height    uint64 `json:"height"`
		}
		_ = json.Unmarshal(response.Result, &result)
		return epicStratumResponse{
			Algorithm: result.Algorithm,
			Height:    result.Height,
		}, nil
	}
	if err := scanner.Err(); err != nil {
		return epicStratumResponse{}, err
	}
	return epicStratumResponse{}, errors.New("Epic Stratum closed before returning a response")
}
