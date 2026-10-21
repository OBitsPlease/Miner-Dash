package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"minerdash/internal/protocol"
)

func TestProbeEpicStratumReadsCurrentAlgorithm(t *testing.T) {
	endpoint := startEpicStratumTestServer(t,
		`{"id":"minerdash-login","jsonrpc":"2.0","method":"login","result":"ok"}`,
		`{"id":"minerdash-status","jsonrpc":"2.0","method":"getjobtemplate","result":{"height":3733001,"algorithm":"randomx"}}`,
	)
	status, err := probeEpicJobTemplate(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if status.Algorithm != "randomx" || status.Height != 3733001 || status.Reason != "" {
		t.Fatalf("Epic status = %#v", status)
	}
}

func TestProbeEpicStratumSendsLoginAndJobRequestWithoutWaiting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		scanner := bufio.NewScanner(connection)
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"method":"login"`) {
			return
		}
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"method":"getjobtemplate"`) {
			return
		}
		_, _ = fmt.Fprintln(connection, `{"id":"minerdash-login","jsonrpc":"2.0","result":{"status":"ok"}}`)
		_, _ = fmt.Fprintln(connection, `{"id":"minerdash-status","jsonrpc":"2.0","result":{"height":3733002,"algorithm":"randomx"}}`)
	}()

	status, err := probeEpicJobTemplate(context.Background(), "stratum+tcp://"+listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if status.Algorithm != "randomx" || status.Height != 3733002 {
		t.Fatalf("Epic status = %#v", status)
	}
}

func TestProbeEpicStratumReportsSyncingState(t *testing.T) {
	endpoint := startEpicStratumTestServer(t,
		`{"id":"minerdash-login","jsonrpc":"2.0","result":"ok"}`,
		`{"id":"minerdash-status","jsonrpc":"2.0","method":"getjobtemplate","error":{"code":-32701,"message":"Node is syncing - Please wait"}}`,
	)
	status, err := probeEpicJobTemplate(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.Reason, "syncing") || status.Algorithm != "" {
		t.Fatalf("Epic status = %#v", status)
	}
}

func TestProbeEpicStratumIgnoresBroadcastJob(t *testing.T) {
	endpoint := startEpicStratumTestServer(t,
		`{"id":"minerdash-login","jsonrpc":"2.0","result":"ok"}`,
		`{"id":"Stratum","jsonrpc":"2.0","method":"job","params":{"height":1,"algorithm":"progpow"}}`,
		`{"id":"minerdash-status","jsonrpc":"2.0","method":"getjobtemplate","result":{"height":2,"algorithm":"cuckoo"}}`,
	)
	status, err := probeEpicJobTemplate(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if status.Algorithm != "cuckoo" || status.Height != 2 {
		t.Fatalf("Epic status = %#v", status)
	}
}

func TestValidateResolvedConfigurationAllowsManagedFallback(t *testing.T) {
	configuration := protocol.ResolvedConfiguration{
		Miner:  protocol.MinerDefinition{Profile: "epic"},
		Wallet: protocol.Wallet{Address: "epic"},
		Pool:   protocol.Pool{URL: "stratum+tcp://epic.invalid:3416"},
		Fallback: &protocol.ResolvedWorkload{
			Miner:  protocol.MinerDefinition{ManagedBinary: true},
			Wallet: protocol.Wallet{Address: "qrl"},
			Pool:   protocol.Pool{URL: "stratum+tcp://qrl.invalid:3333"},
		},
	}
	if err := validateResolvedConfiguration(configuration); err != nil {
		t.Fatalf("managed fallback validation failed: %v", err)
	}
}

func TestEpicProbeFailuresUseLongerFailoverGracePeriod(t *testing.T) {
	if delay := epicFailoverWait(10, errors.New("timeout")); delay != epicProbeFailureDelay {
		t.Fatalf("probe failure delay = %v, want %v", delay, epicProbeFailureDelay)
	}
	if delay := epicFailoverWait(10, nil); delay != 2*time.Second {
		t.Fatalf("algorithm mismatch delay = %v, want 2s", delay)
	}
}

func TestEpicPrimaryHashrateOverridesTransientProbeFailure(t *testing.T) {
	timeout := errors.New("timeout")
	if !epicPrimaryHashing(false, protocol.MinerState{Running: true, Hashrate: 11463}, timeout) {
		t.Fatal("active Epic hashrate did not override a transient probe failure")
	}
	for _, test := range []struct {
		name     string
		fallback bool
		miner    protocol.MinerState
		err      error
	}{
		{name: "fallback active", fallback: true, miner: protocol.MinerState{Running: true, Hashrate: 15000}, err: timeout},
		{name: "zero hashrate", miner: protocol.MinerState{Running: true}, err: timeout},
		{name: "probe succeeded", miner: protocol.MinerState{Running: true, Hashrate: 11463}},
	} {
		if epicPrimaryHashing(test.fallback, test.miner, test.err) {
			t.Fatalf("%s unexpectedly treated Epic as active", test.name)
		}
	}
}

func startEpicStratumTestServer(t *testing.T, responses ...string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		scanner := bufio.NewScanner(connection)
		for range 2 {
			if !scanner.Scan() {
				return
			}
		}
		for _, response := range responses {
			_, _ = fmt.Fprintln(connection, response)
		}
	}()
	return "stratum+tcp://" + listener.Addr().String()
}
