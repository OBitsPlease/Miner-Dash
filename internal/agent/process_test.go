package agent

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestEpicMinerArgumentsPreserveSelectedStratumURL(t *testing.T) {
	for _, poolURL := range []string{
		"stratum+tcp://192.0.2.44:3416",
		"stratum+ssl://pool.invalid:3416",
		"stratum+tls://[2001:db8::44]:3416",
	} {
		t.Run(poolURL, func(t *testing.T) {
			arguments := resolveArguments([]string{"--stratum", "{POOL}", "--large-pages"}, map[string]string{
				"{POOL}": poolURL,
			})
			want := []string{"--stratum", poolURL, "--large-pages"}
			for index := range want {
				if arguments[index] != want[index] {
					t.Fatalf("Epic arguments = %#v, want %#v", arguments, want)
				}
			}
		})
	}
}

func TestEpicStratumLoginUsesSanitizedWorkerName(t *testing.T) {
	if got := stratumLogin(" CPU Rig #01 "); got != "CPU-Rig--01" {
		t.Fatalf("stratumLogin() = %q", got)
	}
	if got := stratumLogin("..."); got != "minerdash" {
		t.Fatalf("blank stratumLogin() = %q", got)
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

func TestTRexStatsIncludePerGPUHashrates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"accepted_count":41,
			"rejected_count":2,
			"hashrate":88700000,
			"gpus":[
				{"device_id":0,"gpu_id":0,"name":"NVIDIA GeForce RTX 3060 Ti","hashrate":44300000},
				{"device_id":1,"gpu_id":1,"name":"NVIDIA GeForce RTX 3060 Ti","hashrate":44400000}
			]
		}`))
	}))
	defer server.Close()
	state := protocol.MinerState{Running: true}
	readTRexStats(server.URL, &state)
	if state.Hashrate != 88700000 || state.HashrateUnit != "H/s" || len(state.Devices) != 2 {
		t.Fatalf("T-Rex state = %#v", state)
	}
	if state.AcceptedShares != 41 || state.RejectedShares != 2 {
		t.Fatalf("T-Rex shares = %#v", state)
	}
	if state.Devices[1].Index != 1 || state.Devices[1].Kind != "GPU" || state.Devices[1].Hashrate != 44400000 {
		t.Fatalf("T-Rex device = %#v", state.Devices[1])
	}
}

func TestTRexAPIArgumentsAreAddedOnlyWhenMissing(t *testing.T) {
	added := ensureTRexAPIArguments([]string{"-o", "stratum+tcp://pool:1234"})
	if len(added) != 4 || added[2] != "--api-bind-http" || added[3] != "127.0.0.1:4067" {
		t.Fatalf("added arguments = %#v", added)
	}
	existing := []string{"-o", "stratum+tcp://pool:1234", "--api-bind-http", "127.0.0.1:5000"}
	if result := ensureTRexAPIArguments(existing); len(result) != len(existing) {
		t.Fatalf("operator bind was overridden: %#v", result)
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

func TestEpicStatsUseNewestHeadlessHashrate(t *testing.T) {
	state := protocol.MinerState{Running: true}
	readEpicStats(`
Oct 07 00:01:02.100 INFO Mining: RandomX at 14872 hps (hashes per second)
Oct 07 00:01:04.100 INFO Mining: RandomX at 15123.5 hps (hashes per second)
`, &state)
	if state.Hashrate != 15123.5 || state.HashrateUnit != "H/s" {
		t.Fatalf("Epic state = %#v", state)
	}
}

func TestEpicStatsIgnoreUnrelatedMinerOutput(t *testing.T) {
	state := protocol.MinerState{Running: true}
	readEpicStats("Starting Epic Miner\nWaiting for job\n", &state)
	if state.Hashrate != 0 || state.HashrateUnit != "" {
		t.Fatalf("Epic state = %#v", state)
	}
}

func TestMinerFailureReasonUsesActionableOutput(t *testing.T) {
	reason := minerFailureReason("startup\nNot supported algo: progpow\n", errors.New("exit status 1"))
	if !strings.Contains(reason, "Not supported algo: progpow") {
		t.Fatalf("failure reason omitted miner output: %q", reason)
	}
}
