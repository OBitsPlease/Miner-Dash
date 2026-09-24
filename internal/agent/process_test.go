package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"minerdash/internal/protocol"
)

func TestBoundedBufferKeepsNewestOutput(t *testing.T) {
	buffer := &boundedBuffer{limit: 8}
	if _, err := buffer.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("67890")); err != nil {
		t.Fatal(err)
	}
	if actual := buffer.String(); actual != "34567890" {
		t.Fatalf("buffer = %q, want newest eight bytes", actual)
	}
	if _, err := buffer.Write([]byte("abcdefghijkl")); err != nil {
		t.Fatal(err)
	}
	if actual := buffer.String(); actual != "efghijkl" {
		t.Fatalf("buffer after oversized write = %q", actual)
	}
}

func TestResolvedArgumentsAreSubstitutedWithoutShell(t *testing.T) {
	arguments := []string{"--url", "{POOL}", "--user={WALLET}.{WORKER}"}
	replacements := map[string]string{
		"{POOL}":   "stratum+tcp://pool.invalid:3333",
		"{WALLET}": "wallet",
		"{WORKER}": "rig-01",
	}
	resolved := resolveArguments(arguments, replacements)
	if resolved[1] != "stratum+tcp://pool.invalid:3333" || resolved[2] != "--user=wallet.rig-01" {
		t.Fatalf("resolved arguments = %#v", resolved)
	}
}

func TestProfileAndFlightSheetArgumentsAreBothResolved(t *testing.T) {
	profile := Profile{Args: []string{"-o", "{POOL}", "-u", "{WALLET}.{WORKER}"}}
	arguments := resolvedCommandArguments(profile, []string{"-a", "{COIN}"}, map[string]string{
		"{POOL}": "stratum+tcp://pool.invalid:3333", "{WALLET}": "wallet",
		"{WORKER}": "rig-01", "{COIN}": "randomx",
	})
	want := []string{"-o", "stratum+tcp://pool.invalid:3333", "-u", "wallet.rig-01", "-a", "randomx"}
	if len(arguments) != len(want) {
		t.Fatalf("arguments = %#v", arguments)
	}
	for index := range want {
		if arguments[index] != want[index] {
			t.Fatalf("arguments = %#v, want %#v", arguments, want)
		}
	}
}

func TestArgumentReplacementDoesNotRecursivelyExpandValues(t *testing.T) {
	resolved := resolveArguments([]string{"{WALLET}"}, map[string]string{
		"{WALLET}": "literal-{PASSWORD}", "{PASSWORD}": "secret",
	})
	if resolved[0] != "literal-{PASSWORD}" {
		t.Fatalf("replacement recursively expanded a value: %#v", resolved)
	}
}

func TestPoolEndpointRemovesScheme(t *testing.T) {
	if got := poolEndpoint("stratum+tcp://192.0.2.79:3032"); got != "192.0.2.79:3032" {
		t.Fatalf("poolEndpoint() = %q", got)
	}
}

func TestXMRigStatsIncludePerThreadHashrates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{
			"hashrate":{"total":[1250.5,1200,1190],"threads":[[600.25,590,580],[650.25,610,600]]},
			"connection":{"accepted":12,"rejected":1}
		}`))
	}))
	defer server.Close()
	state := protocol.MinerState{Running: true}
	readXMRigStats(server.URL, "local-token", &state)
	if state.Hashrate != 1250.5 || len(state.Devices) != 2 {
		t.Fatalf("XMRig state = %#v", state)
	}
	if state.Devices[1].Kind != "CPU thread" || state.Devices[1].Hashrate != 650.25 {
		t.Fatalf("second thread = %#v", state.Devices[1])
	}
}

func TestXMRigStatsFallBackToBackendsForPerThreadHashrates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/2/summary":
			_, _ = w.Write([]byte(`{
				"hashrate":{"total":[1250.5,1200,1190],"threads":null},
				"connection":{"accepted":12,"rejected":1}
			}`))
		case "/2/backends":
			_, _ = w.Write([]byte(`[
				{"type":"cpu","threads":[{"hashrate":[600.25,590,580]},{"hashrate":[650.25,610,600]}]},
				{"type":"opencl","threads":[{"hashrate":[100,90,80]}]}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	state := protocol.MinerState{Running: true}
	readXMRigStats(server.URL+"/2/summary", "local-token", &state)
	if state.Hashrate != 1250.5 || len(state.Devices) != 2 {
		t.Fatalf("XMRig state = %#v", state)
	}
	if state.Devices[1].Kind != "CPU thread" || state.Devices[1].Hashrate != 650.25 {
		t.Fatalf("second thread = %#v", state.Devices[1])
	}
}

func TestMiniZStatsIncludePerGPUHashrates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"result":[
				{"gpuid":0,"name":"RTX 4060 Ti","speed_sps":49.1,"accepted_shares":12,"rejected_shares":1},
				{"gpuid":1,"name":"RTX 3060 Ti","speed_sps":39.4,"accepted_shares":10,"rejected_shares":0}
			]
		}`))
	}))
	defer server.Close()
	state := protocol.MinerState{Running: true}
	readMiniZStats(server.URL, &state)
	if state.Hashrate != 88.5 || state.HashrateUnit != "Sol/s" || len(state.Devices) != 2 {
		t.Fatalf("miniZ state = %#v", state)
	}
	if state.AcceptedShares != 22 || state.RejectedShares != 1 || state.Devices[1].Hashrate != 39.4 {
		t.Fatalf("miniZ device stats = %#v", state)
	}
}

func TestSRBMinerStatsIncludeHashrateAndShares(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"algorithms":[{
				"algorithm":"randomx",
				"hashrate":{"cpu":{"thread0":8500.25,"thread1":8625.25,"total":17125.5},"gpu":{"total":0}},
				"shares":{"accepted":24,"rejected":1}
			}]
		}`))
	}))
	defer server.Close()
	state := protocol.MinerState{Running: true}
	readSRBMinerStats(server.URL, &state)
	if state.Hashrate != 17125.5 || state.HashrateUnit != "H/s" || len(state.Devices) != 2 {
		t.Fatalf("SRBMiner state = %#v", state)
	}
	if state.AcceptedShares != 24 || state.RejectedShares != 1 || state.Devices[1].Hashrate != 8625.25 {
		t.Fatalf("SRBMiner device stats = %#v", state)
	}
}
