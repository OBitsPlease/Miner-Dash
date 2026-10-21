package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCMPHardwareFallbackDoesNotDependOnMinerstat(t *testing.T) {
	provider := newDiscoverProvider()
	catalog := provider.cmpHardwareCatalog()
	if len(catalog) != 8 {
		t.Fatalf("CMP fallback count = %d", len(catalog))
	}
	for _, device := range catalog {
		if device.Type != "gpu" || len(device.Benchmarks) == 0 {
			t.Fatalf("CMP fallback device = %#v", device)
		}
	}
}

func TestParseMinerstatCMPBenchmarks(t *testing.T) {
	body := `Performance per algorithm/algorithm/etchash30.59 MH/s82 W/algorithm/kawpow10.75 MH/s80 W/algorithm/xelishash3.62 kH/s65 W/algorithm/cuckaroo292.5 H/s69 W`
	benchmarks := parseMinerstatCMPBenchmarks(body)
	if len(benchmarks) != 4 {
		t.Fatalf("benchmark count = %d", len(benchmarks))
	}
	found := map[string]calcBenchmark{}
	for _, benchmark := range benchmarks {
		found[normalizeHardwareAlgorithm(benchmark.Algorithm)] = benchmark
	}
	if found["etchash"].Hashrate != 30.59e6 || found["xelishash"].Hashrate != 3.62e3 {
		t.Fatalf("parsed benchmark values = %#v", found)
	}
}

func TestCMPFallbackRemainsAvailableWithoutPersistentCache(t *testing.T) {
	provider := newDiscoverProvider()
	devices := provider.cmpHardwareCatalog()
	if len(devices) != 8 || len(devices[0].Benchmarks) == 0 {
		t.Fatalf("CMP fallback catalog = %#v", devices)
	}
}

func TestCMPHardwareCatalogIncludesRequestedCardsAndBenchmarks(t *testing.T) {
	catalog := cmpHardwareCatalog()
	wanted := map[string]bool{
		"NVIDIA CMP 30HX":            false,
		"NVIDIA CMP 40HX":            false,
		"NVIDIA CMP 50HX":            false,
		"NVIDIA CMP 100-210 (100HX)": false,
		"NVIDIA CMP 70HX":            false,
		"NVIDIA CMP 90HX":            false,
		"NVIDIA CMP 170HX (8 GB)":    false,
		"NVIDIA CMP 170HX (10 GB)":   false,
	}
	for _, device := range catalog {
		if _, ok := wanted[device.Name]; !ok {
			continue
		}
		wanted[device.Name] = true
		if device.Type != "gpu" || device.Vendor != "NVIDIA" || len(device.Benchmarks) == 0 {
			t.Errorf("invalid CMP device %#v", device)
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("missing CMP device %s", name)
		}
	}
}

func TestCalculatorRoutesRegistered(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPServer(store, log.New(testWriter{t}, "", 0))
	for _, path := range []string{"/api/v1/discover/calc-hardware", "/api/v1/discover/calc-algorithms"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer admin")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code == http.StatusNotFound {
			t.Fatalf("calculator route %s returned 404", path)
		}
	}
}

func TestCalculatorCapabilityClassesIncludeCompleteCatalog(t *testing.T) {
	cpu := map[string]bool{}
	gpu := map[string]bool{}
	for _, miner := range minerCatalog {
		catalog, err := parseMinerAlgorithmCatalog(bundledMinerAlgorithms)
		if err != nil {
			t.Fatal(err)
		}
		entry, ok := catalog.entry(miner.ID)
		if !ok {
			continue
		}
		for _, algorithm := range entry.Algorithms {
			for _, hardware := range miner.Hardware {
				if hardware == "CPU" {
					cpu[normalizeHardwareAlgorithm(algorithm)] = true
				} else {
					gpu[normalizeHardwareAlgorithm(algorithm)] = true
				}
			}
		}
	}
	for _, algorithm := range []string{"flex", "xelishashv3", "randomx", "rx/veil"} {
		if !cpu[normalizeHardwareAlgorithm(algorithm)] {
			t.Errorf("CPU calculator is missing %s", algorithm)
		}
	}
	for _, algorithm := range []string{"kawpow", "progpow-veil", "xelishashv3", "karlsenhashv2"} {
		if !gpu[normalizeHardwareAlgorithm(algorithm)] {
			t.Errorf("GPU calculator is missing %s", algorithm)
		}
	}
}

func TestParseMiningPoolStatsRowsAcceptsFeedPreamble(t *testing.T) {
	body := `{"data":[{"name":"Bitcoin","symbol":"BTC","algo":"SHA-256","page":"bitcoin","typ":"ASIC","unit":"H/s","pools":152,"pr":83647,"mc":1681033561725.1,"v24":15010400649,"c7d":-1.97,"e24":509.4,"bt":530,"target":600,"diff":"132716002350731.3","diff7":[120000000000000,125000000000000],"hashrate":1.07e+21,"ph":1.13e+21},{"name":"","symbol":"ZZZ"}],"time":1791746587}`
	rows, err := parseMiningPoolStatsRows(body)
	if err != nil {
		t.Fatalf("parse rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected nameless rows to be dropped, got %d rows", len(rows))
	}
	if rows[0].Difficulty.float() != 132716002350731.3 {
		t.Fatalf("expected string difficulty to parse, got %v", rows[0].Difficulty.float())
	}
	if rows[0].Pools != 152 {
		t.Fatalf("expected 152 pools, got %d", rows[0].Pools)
	}
}

func TestParseMiningPoolStatsRowsRejectsEmptyFeed(t *testing.T) {
	if _, err := parseMiningPoolStatsRows(`{"data":[]}`); err == nil {
		t.Fatal("expected an error when the feed contains no coins")
	}
	if _, err := parseMiningPoolStatsRows("not json"); err == nil {
		t.Fatal("expected an error when the feed is not JSON")
	}
}

func TestBuildPOWCoinsMergesGeckoChangesAndComputesDifficultyChange(t *testing.T) {
	hour, day := 1.5, -2.5
	rows := []mpsRow{
		{Name: "Bitcoin", Symbol: "btc", Algo: "SHA-256", Page: "bitcoin", Price: 100, MarketCap: 500, Change7d: -1.97, Difficulty: 110, Difficulty7d: []flexFloat{100, 105}, Hashrate: 10},
		{Name: "Monero", Symbol: "XMR", Algo: "RandomX", Page: "monero", Price: 150, MarketCap: 900, Difficulty: 0, Difficulty7d: nil},
	}
	coins := buildPOWCoins(rows, map[string]geckoChange{"BTC": {Change1h: &hour, Change24h: &day}})
	if len(coins) != 2 {
		t.Fatalf("expected 2 coins, got %d", len(coins))
	}
	if coins[0].Symbol != "XMR" {
		t.Fatalf("expected market-cap ordering to put XMR first, got %s", coins[0].Symbol)
	}
	bitcoin := coins[1]
	if bitcoin.Symbol != "BTC" {
		t.Fatalf("expected the symbol to be upper-cased, got %s", bitcoin.Symbol)
	}
	if bitcoin.PageURL != "https://miningpoolstats.stream/bitcoin" {
		t.Fatalf("unexpected page URL %q", bitcoin.PageURL)
	}
	if bitcoin.Change1h == nil || *bitcoin.Change1h != 1.5 {
		t.Fatalf("expected the CoinGecko 1h change to be merged, got %v", bitcoin.Change1h)
	}
	if bitcoin.Change7d == nil || *bitcoin.Change7d != -1.97 {
		t.Fatalf("expected the MiningPoolStats 7d change, got %v", bitcoin.Change7d)
	}
	if bitcoin.Diff7dChange == nil || *bitcoin.Diff7dChange != 10 {
		t.Fatalf("expected a 10%% difficulty change, got %v", bitcoin.Diff7dChange)
	}
	if coins[0].Diff7dChange != nil {
		t.Fatal("expected no difficulty change when history is missing")
	}
}

const calendarFixture = `<div class="box-body">
<div class="info-box box-event box-event-halving"><span class="info-box-icon bg-gray"><span style="position:relative;top:12px;">112<br>Days</span><span class="info-box-text" style="position:relative;top:22px;">2027-01-31</span></span><div class="info-box-content"><span class="pull-right-container pull-right"><small class="label pull-right bg-yellow" style="font-size:12px;">Halving</small><BR></span><div style="margin-top:5px;"><img class="lozad" src="data:image/gif;base64,R0l" data-src="ico/adventurecoin.webp" alt="AdventureCoin" style="height:32px;"><span style="display: inline;"><div style="display: inline;"><a href="adventurecoin">AdventureCoin (ADVC)</a></div><div style="line-height:12px">Block : 300000</div></span></div><span>Block reward reduced from <b>300</b> to <b>150</b></span></div></div><div class="info-box box-event box-event-algo_change"><span class="info-box-icon bg-gray"><span style="position:relative;top:12px;">10<br>Days</span><span class="info-box-text" style="position:relative;top:22px;">2020-01-02</span></span><div class="info-box-content"><span class="pull-right-container pull-right"><small class="label pull-right bg-aqua" style="font-size:12px;">Algo Change</small><BR></span><div style="margin-top:5px;"><img class="lozad" data-src="ico/oldcoin.webp" alt="OldCoin"><span><div style="display: inline;"><a href="oldcoin">OldCoin (OLD)</a></div></span></div><span>Switching to <b>KawPow</b></span></div></div>
</div>`

func TestParseCalendarEventsSplitsUpcomingAndPast(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	payload := parseCalendarEvents(calendarFixture, now)
	if len(payload.Upcoming) != 1 {
		t.Fatalf("expected 1 upcoming event, got %d", len(payload.Upcoming))
	}
	if len(payload.Past) != 1 {
		t.Fatalf("expected 1 past event, got %d", len(payload.Past))
	}
	event := payload.Upcoming[0]
	if event.Name != "AdventureCoin" || event.Symbol != "ADVC" {
		t.Fatalf("unexpected coin identity %q / %q", event.Name, event.Symbol)
	}
	if event.EventType != "halving" || event.Label != "Halving" {
		t.Fatalf("unexpected event type %q / label %q", event.EventType, event.Label)
	}
	if event.Date != "2027-01-31" {
		t.Fatalf("unexpected date %q", event.Date)
	}
	if event.Days != 112 {
		t.Fatalf("expected the countdown to be recomputed from the date, got %d", event.Days)
	}
	if event.BlockInfo != "Block : 300000" {
		t.Fatalf("unexpected block info %q", event.BlockInfo)
	}
	if event.Description != "Block reward reduced from 300 to 150" {
		t.Fatalf("unexpected description %q", event.Description)
	}
	if event.IconURL != "https://miningpoolstats.stream/ico/adventurecoin.webp" {
		t.Fatalf("unexpected icon %q", event.IconURL)
	}
	if event.CoinURL != "https://miningpoolstats.stream/adventurecoin" {
		t.Fatalf("unexpected coin URL %q", event.CoinURL)
	}
	if payload.Past[0].Symbol != "OLD" {
		t.Fatalf("expected the past event to be OldCoin, got %q", payload.Past[0].Symbol)
	}
}

func boardRow(icon, topicURL, title string) string {
	return fmt.Sprintf(`<tr><td class="windowbg icon2"><img src="https://bitcointalk.org/Themes/custom1/images/%s" alt="" /></td>`+
		`<td class="windowbg2"><span id="msg_123"><a href="%s">%s</a></span></td></tr>`, icon, topicURL, title)
}

func TestParseBitcointalkCandidatesSkipsStickyLockedAndNonAnnouncements(t *testing.T) {
	body := strings.Join([]string{
		boardRow("xx.gif", "https://bitcointalk.org/index.php?topic=1.0", "[ANN] GoodCoin - a mineable coin"),
		boardRow("sticky.gif", "https://bitcointalk.org/index.php?topic=2.0", "[ANN] StickyCoin"),
		boardRow("locked.gif", "https://bitcointalk.org/index.php?topic=3.0", "[ANN] LockedCoin"),
		boardRow("xx.gif", "https://bitcointalk.org/index.php?topic=4.0", "Just a discussion thread"),
		boardRow("xx.gif", "https://bitcointalk.org/index.php?topic=5.0", "[RE-ANN] SecondCoin"),
	}, "\n")
	candidates := parseBitcointalkCandidates(body)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(candidates), candidates)
	}
	if candidates[0].URL != "https://bitcointalk.org/index.php?topic=1.0" {
		t.Fatalf("unexpected first candidate %q", candidates[0].URL)
	}
	if candidates[1].Title != "[RE-ANN] SecondCoin" {
		t.Fatalf("unexpected second candidate %q", candidates[1].Title)
	}
}

func threadFixture(date, post string) string {
	return fmt.Sprintf(`<div class="subject" id="subject_1"><a href="#">title</a></div>`+
		`<div class="smalltext">%s</div><hr /><div class="post">%s</div>`, date, post)
}

func TestBuildListingFromThreadKeepsRecentProofOfWorkAnnouncements(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	candidate := listingCandidate{Title: "[ANN] GoodCoin (GOOD) - CPU mineable", URL: "https://bitcointalk.org/index.php?topic=1.0"}
	post := `Algorithm: RandomX<br /><a href="https://goodcoin.org">site</a> <a href="https://github.com/good/coin">code</a> <a href="https://explorer.goodcoin.org">explorer</a>`
	listing, ok := buildListingFromThread(candidate, threadFixture("March 06, 2026, 06:23:00 PM", post), now)
	if !ok {
		t.Fatal("expected a recent proof-of-work announcement to be kept")
	}
	if listing.Symbol != "GOOD" {
		t.Fatalf("unexpected symbol %q", listing.Symbol)
	}
	if listing.Name != "GoodCoin" {
		t.Fatalf("unexpected coin name %q", listing.Name)
	}
	if listing.Title != candidate.Title {
		t.Fatalf("expected the raw thread title to be retained, got %q", listing.Title)
	}
	if listing.Algo != "RandomX" || listing.AlgoSource != "medium" {
		t.Fatalf("unexpected algorithm %q from %q", listing.Algo, listing.AlgoSource)
	}
	if listing.Website != "https://goodcoin.org" || listing.GitHub != "https://github.com/good/coin" || listing.Explorer != "https://explorer.goodcoin.org" {
		t.Fatalf("unexpected links %+v", listing)
	}
	if listing.Listed != "March 06, 2026, 06:23:00 PM" {
		t.Fatalf("unexpected listed text %q", listing.Listed)
	}
}

func TestBuildListingFromThreadDropsStaleAndStakingAnnouncements(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	candidate := listingCandidate{Title: "[ANN] OldCoin - GPU mining", URL: "https://bitcointalk.org/index.php?topic=1.0"}
	if _, ok := buildListingFromThread(candidate, threadFixture("March 06, 2026, 06:23:00 PM", "mining"), now); ok {
		t.Fatal("expected an announcement older than seven days to be dropped")
	}
	staking := listingCandidate{Title: "[ANN] StakeCoin - proof of stake only", URL: "https://bitcointalk.org/index.php?topic=2.0"}
	if _, ok := buildListingFromThread(staking, threadFixture("Today at 01:00:00 PM", "staking rewards"), now); ok {
		t.Fatal("expected a staking announcement to be dropped")
	}
}

func TestBuildListingFromThreadInfersAlgorithmFromKeywords(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	candidate := listingCandidate{Title: "[ANN] KawCoin - GPU mineable", URL: "https://bitcointalk.org/index.php?topic=9.0"}
	listing, ok := buildListingFromThread(candidate, threadFixture("Today at 09:00:00 AM", "We use kawpow for fair launch mining."), now)
	if !ok {
		t.Fatal("expected the announcement to be kept")
	}
	if listing.Algo != "KawPow" || listing.AlgoSource != "low" {
		t.Fatalf("expected a low-confidence KawPow match, got %q / %q", listing.Algo, listing.AlgoSource)
	}
}

func TestAnnotateListingsWithMarketRaisesConfidence(t *testing.T) {
	listings := []newListing{
		{Name: "[ANN] Monero - private money", Symbol: "XMR", Algo: "Scrypt", AlgoSource: "low"},
		{Name: "[ANN] Unlisted - brand new", Symbol: "NEW", Algo: "Scrypt", AlgoSource: "low"},
	}
	annotateListingsWithMarket(listings, []mpsRow{{Name: "Monero", Symbol: "XMR", Algo: "RandomX", Price: 150}})
	if listings[0].Algo != "RandomX" || listings[0].AlgoSource != "high" {
		t.Fatalf("expected a directory match to upgrade the algorithm, got %q / %q", listings[0].Algo, listings[0].AlgoSource)
	}
	if listings[0].Price != 150 {
		t.Fatalf("expected the directory price, got %v", listings[0].Price)
	}
	if listings[1].AlgoSource != "low" {
		t.Fatalf("expected unmatched listings to keep their confidence, got %q", listings[1].AlgoSource)
	}
}

func TestListingCoinNameStripsAnnouncementDecoration(t *testing.T) {
	cases := map[string]string{
		"[ANN] Monero - private money":                                   "Monero",
		"[RE-ANN] Ravencoin (RVN)":                                       "Ravencoin",
		"[ANN] Vertcoin: the people":                                     "Vertcoin",
		"[ANN] ConnectCoin � payment network":                            "ConnectCoin",
		"[ANN][TESTNET][PoW] (TNR)Tenero | matmulhash v2 | Privacy coin": "Tenero",
		"[ANN][PoW] Dogecoin":                                            "Dogecoin",
	}

	for title, expected := range cases {
		if actual := listingCoinName(title); actual != expected {
			t.Fatalf("listingCoinName(%q) = %q, expected %q", title, actual, expected)
		}
	}
}

func TestBuildListingFromThreadIgnoresRecentReplies(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	candidate := listingCandidate{Title: "[ANN] OldCoin - GPU mining", URL: "https://bitcointalk.org/index.php?topic=1.0"}
	body := `<tr><div class="smalltext">January 02, 2020, 01:00:00 PM</div><div class="post">KawPow mining</div></tr>` +
		`<tr><div class="smalltext">Today at 09:00:00 AM</div><div class="post">Recent reply</div></tr>`
	if _, ok := buildListingFromThread(candidate, body, now); ok {
		t.Fatal("expected the old first post to win over the recent reply date")
	}
}

func TestDiscoverListingsCacheSurvivesRestartAndExpiresOldRows(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	provider := newDiscoverProvider(t.TempDir())
	provider.saveListings([]newListing{
		{Name: "Recent", Thread: "recent", ListedAt: now.Add(-10 * 24 * time.Hour).Format(time.RFC3339), Source: "Bitcointalk"},
		{Name: "Old", Thread: "old", ListedAt: now.Add(-15 * 24 * time.Hour).Format(time.RFC3339), Source: "Bitcointalk"},
		{Name: "Recently Indexed", Thread: "indexed", ListedAt: now.Add(-time.Hour).Format(time.RFC3339), Source: "MiningPoolStats"},
	})
	restarted := newDiscoverProvider(filepath.Dir(provider.listingCachePath))
	listings := restarted.loadSavedListings(now)
	if len(listings) != 1 || listings[0].Name != "Recent" {
		t.Fatalf("unexpected persisted listings: %+v", listings)
	}
}

func TestMergeSavedListingsKeepsUniqueFallbackRows(t *testing.T) {
	current := []newListing{{Name: "One", Thread: "thread-1"}}
	saved := []newListing{{Name: "One", Thread: "thread-1"}, {Name: "Two", Thread: "thread-2"}}
	merged := mergeSavedListings(current, saved)
	if len(merged) != 2 || merged[1].Name != "Two" {
		t.Fatalf("unexpected merged listings: %+v", merged)
	}
}

func TestParseCalendarEventsFallsBackToRenderedCountdown(t *testing.T) {
	fixture := `<div class="info-box box-event box-event-halving"><span class="info-box-icon bg-gray"><span style="top:12px;">20737<br>Days</span><span class="info-box-text" style="top:22px;"></span></span><div class="info-box-content"><small class="label pull-right bg-yellow">Halving</small><div><img class="lozad" data-src="ico/foxdcoin.webp" alt="FoxDcoin"><span><div><a href="foxdcoin">FoxDcoin (FOXD)</a></div><div>Block : 2100000</div></span></div><span>Block reward reduced from <b>5000</b> to <b>2500</b></span></div></div>`
	payload := parseCalendarEvents(fixture, time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC))
	if len(payload.Upcoming) != 1 {
		t.Fatalf("expected a block-height event to be kept, got %d", len(payload.Upcoming))
	}
	event := payload.Upcoming[0]
	if event.Date != "" {
		t.Fatalf("expected no calendar date, got %q", event.Date)
	}
	if event.Days != 20737 {
		t.Fatalf("expected the rendered countdown to be used, got %d", event.Days)
	}
	if event.Symbol != "FOXD" {
		t.Fatalf("unexpected symbol %q", event.Symbol)
	}
}

func TestExtractListingLinksIgnoresAssets(t *testing.T) {
	post := `<a href="https://vearl.org/og-image.png?v=3">img</a><a href="https://vearl.org">site</a>` +
		`<a href="https://github.com/vearl/core">code</a><a href="https://explorer.vearl.org">explorer</a>`
	website, github, explorer := extractListingLinks(post)
	if website != "https://vearl.org" {
		t.Fatalf("expected the asset URL to be skipped, got %q", website)
	}
	if github != "https://github.com/vearl/core" || explorer != "https://explorer.vearl.org" {
		t.Fatalf("unexpected links %q / %q", github, explorer)
	}
}

func TestDifficultySevenDaysAgoUsesOldestSample(t *testing.T) {
	row := mpsRow{Difficulty7d: []flexFloat{0, 544879460700, 538667652400}}
	if value := row.difficultySevenDaysAgo(); value != 544879460700 {
		t.Fatalf("unexpected sample %v", value)
	}
	if (mpsRow{}).difficultySevenDaysAgo() != 0 {
		t.Fatal("expected a missing history to yield zero")
	}
}

func TestDedupeScanNetworksKeepsOneEntryPerSubnet(t *testing.T) {
	networks := dedupeScanNetworks([]scanNetwork{
		{Name: "Ethernet", CIDR: "192.168.1.0/24", Address: "192.168.1.79"},
		{Name: "Ethernet", CIDR: "192.168.1.0/24", Address: "192.168.1.169"},
		{Name: "Ethernet", CIDR: "192.168.50.0/24", Address: "192.168.50.10"},
	})
	if len(networks) != 2 {
		t.Fatalf("expected two unique subnets, got %d", len(networks))
	}
	if networks[0].Address != "192.168.1.79" {
		t.Fatalf("expected the first address to be kept, got %q", networks[0].Address)
	}
}

func TestFingerprintRatesHiveShellPortAsPossible(t *testing.T) {
	detection := fingerprintDevice("cpu232tr97950x.attlocal.net", []int{22, 4200}, map[int]scanPage{}, "")
	if detection.Platform != "HiveOS" {
		t.Fatalf("unexpected platform %q", detection.Platform)
	}
	if detection.Confidence != "possible" {
		t.Fatalf("expected an open Hive shell port to read as possible, got %q", detection.Confidence)
	}
}

func TestCoinGeckoChangesReusesTheLastGoodResponse(t *testing.T) {
	provider := newDiscoverProvider()
	hour := 1.5
	provider.lastGecko = map[string]geckoChange{"BTC": {Change1h: &hour}}
	// A cancelled context forces every upstream request to fail.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	changes := provider.coinGeckoChanges(ctx)
	if len(changes) != 1 || changes["BTC"].Change1h == nil || *changes["BTC"].Change1h != 1.5 {
		t.Fatalf("expected the previous response to be reused, got %+v", changes)
	}
}

func TestParseWhatToMineCoinsSkipsLaggingAndIncompleteCoins(t *testing.T) {
	body := `{"coins":{"Ravencoin":{"id":234,"tag":"rvn","algorithm":"KawPow","block_time":"60.0","block_reward":2500,"block_reward24":2500,"nethash":5000000,"difficulty":1000,"exchange_rate":0.0000002,"lagging":false},` +
		`"Stale":{"tag":"STL","algorithm":"Ethash","block_time":"13.0","block_reward24":2,"nethash":10,"exchange_rate":0.1,"lagging":true},` +
		`"NoHash":{"tag":"NH","algorithm":"Ethash","block_time":"13.0","block_reward24":2,"nethash":0,"exchange_rate":0.1,"lagging":false}}}`
	coins, err := parseWhatToMineCoins(body, 50000)
	if err != nil {
		t.Fatalf("parse coins: %v", err)
	}
	if len(coins) != 1 {
		t.Fatalf("expected 1 usable coin, got %d", len(coins))
	}
	coin := coins[0]
	if coin.Tag != "RVN" {
		t.Fatalf("expected the tag to be upper-cased, got %q", coin.Tag)
	}
	if coin.PriceUSD != 0.0000002*50000 {
		t.Fatalf("expected the BTC exchange rate to convert to USD, got %v", coin.PriceUSD)
	}
	if coin.CoinsPerDay != 86400/60.0*2500 {
		t.Fatalf("unexpected network emission %v", coin.CoinsPerDay)
	}
	if coin.Unit != "H/s" {
		t.Fatalf("unexpected unit %q", coin.Unit)
	}
}

func TestCalcUnitForAlgorithmUsesSolutionsForEquihash(t *testing.T) {
	if calcUnitForAlgorithm("Equihash") != "Sol/s" {
		t.Fatal("expected Equihash to report Sol/s")
	}
	if calcUnitForAlgorithm("RandomX") != "H/s" {
		t.Fatal("expected RandomX to report H/s")
	}
}

func TestNewMiningPoolStatsCalcCoinsOnlyAddsUnknownCoins(t *testing.T) {
	rows := []mpsRow{
		{Name: "Ravencoin", Symbol: "RVN", Algo: "KawPow", Hashrate: 10, Emissions24: 5, Price: 0.02},
		{Name: "Brand New", Symbol: "NEW", Algo: "Yespower", Hashrate: 10, Emissions24: 5, Price: 0.5, BlockTime: 120, Age: 86400},
		{Name: "No Price", Symbol: "NP", Algo: "Yespower", Hashrate: 10, Emissions24: 5},
	}

	coins := newMiningPoolStatsCalcCoins(rows, map[string]bool{"RVN|KawPow": true})
	if len(coins) != 1 {
		t.Fatalf("expected 1 new coin, got %d", len(coins))
	}
	if coins[0].Tag != "NEW" || coins[0].IsNew || !coins[0].RecentlyIndexed || coins[0].Source != "miningpoolstats" {
		t.Fatalf("unexpected coin %+v", coins[0])
	}
}

func TestBuildWhatToMineProfitURLsConvertsHashrateUnits(t *testing.T) {
	urls := buildWhatToMineProfitURLs([]calcProfitInput{
		{Algorithm: "KawPow", Hashrate: 150e6, Watts: 300},
		{Algorithm: "RandomX", Hashrate: 32e3, Watts: 180},
	})
	coinsURL, err := url.Parse(urls[whatToMineCoins])
	if err != nil {
		t.Fatalf("parse coins URL: %v", err)
	}
	if query := coinsURL.Query(); query.Get("kpw") != "true" || query.Get("factor[kpw_hr]") != "150" || query.Get("factor[kpw_p]") != "300" {
		t.Fatalf("unexpected GPU query %q", coinsURL.RawQuery)
	}
	cpuURL, err := url.Parse(urls[whatToMineCPU])
	if err != nil {
		t.Fatalf("parse CPU URL: %v", err)
	}
	if query := cpuURL.Query(); query.Get("rmx") != "true" || query.Get("factor[rmx_hr]") != "32" || query.Get("factor[rmx_p]") != "180" {
		t.Fatalf("unexpected CPU query %q", cpuURL.RawQuery)
	}
}

func TestParseWhatToMineProfitabilityKeepsNicehashAndExactRewards(t *testing.T) {
	body := `{"coins":{"Ravencoin":{"tag":"RVN","algorithm":"KawPow","estimated_rewards24":"12.5","btc_revenue24":"0.00001","block_reward":"2500","status":"Active","listed":true},` +
		`"Nicehash-KawPow":{"tag":"NICEHASH","algorithm":"KawPow","estimated_rewards24":"0","btc_revenue24":"0.00002","status":"Active","listed":true},` +
		`"Monero":{"tag":"XMR","algorithm":"RandomX","estimated_rewards24":"0.1","btc_revenue24":"0.00003"}}}`
	results, err := parseWhatToMineProfitability(body, []calcProfitInput{{Algorithm: "KawPow", Hashrate: 1}}, 100000)
	if err != nil {
		t.Fatalf("parse profitability: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected Ravencoin and Nicehash, got %+v", results)
	}
	var ravencoin calcProfitResult
	for _, result := range results {
		if result.Name == "Ravencoin" {
			ravencoin = result
		}
	}
	if ravencoin.RevenueUSD != ravencoin.BTCRevenue*100000 || ravencoin.EstimatedRewards != 12.5 {
		t.Fatalf("unexpected exact profitability values %+v", ravencoin)
	}
}

func TestCustomProfitabilityScalesBeyondReportedNetworkHashrate(t *testing.T) {
	coins := []calcCoin{{Name: "Oversubscribed", Algorithm: "kawpow", NetworkHashrate: 100, CoinsPerDay: 10, PriceUSD: 1, Source: "miningpoolstats", RecentlyIndexed: true}}
	results := customProfitabilityResults(coins, []calcProfitInput{{Algorithm: "kawpow", Hashrate: 200}})
	if len(results) != 1 || results[0].EstimatedRewards != 20 {
		t.Fatalf("network-share cap still applied: %+v", results)
	}
}

func TestCustomProfitabilityScalesWithAdditionalHardwareHashrate(t *testing.T) {
	coins := []calcCoin{{Name: "Test Coin", Algorithm: "kawpow", NetworkHashrate: 1000, CoinsPerDay: 100, PriceUSD: 1, Source: "miningpoolstats", RecentlyIndexed: true}}
	one := customProfitabilityResults(coins, []calcProfitInput{{Algorithm: "kawpow", Hashrate: 100}})
	two := customProfitabilityResults(coins, []calcProfitInput{{Algorithm: "kawpow", Hashrate: 200}})
	if len(one) != 1 || len(two) != 1 || two[0].RevenueUSD != one[0].RevenueUSD*2 {
		t.Fatalf("additional hardware did not scale profitability: one=%+v two=%+v", one, two)
	}
}

func TestCustomProfitabilityOnlyAddsNewCoinsForFixedAlgorithms(t *testing.T) {
	coins := []calcCoin{
		{Name: "Old RandomX", Algorithm: "RandomX", NetworkHashrate: 100, CoinsPerDay: 10, PriceUSD: 1, Source: "miningpoolstats"},
		{Name: "Recently Indexed RandomX", Algorithm: "RandomX", NetworkHashrate: 100, CoinsPerDay: 10, PriceUSD: 1, Source: "miningpoolstats", RecentlyIndexed: true},
		{Name: "Dynamic", Algorithm: "Yespower", NetworkHashrate: 100, CoinsPerDay: 10, PriceUSD: 1, Source: "miningpoolstats"},
	}
	results := customProfitabilityResults(coins, []calcProfitInput{
		{Algorithm: "RandomX", Hashrate: 10},
		{Algorithm: "Yespower", Hashrate: 10},
	})
	if len(results) != 2 || results[0].Name != "Recently Indexed RandomX" || results[0].IsNew || results[1].Name != "Dynamic" {
		t.Fatalf("unexpected custom results %+v", results)
	}
}

func TestApplyNonKYCPriceUsesBestBidAfterTradingFee(t *testing.T) {
	result := calcProfitResult{EstimatedRewards: 2284}
	netBidUSD := 0.0034465 * (1 - nonKYCFeeRate)
	applyNonKYCPrice(&result, netBidUSD, "NIR/USDT", 83333)
	if difference := result.RevenueUSD - 7.856062388; difference < -0.000000001 || difference > 0.000000001 {
		t.Fatalf("expected the NonKYC sell total from the screenshot, got %.12f", result.RevenueUSD)
	}
	if result.PriceUSD != netBidUSD || result.Exchange != "NonKYC" {
		t.Fatalf("unexpected executable price metadata %+v", result)
	}
	if !strings.Contains(result.Status, "0.2% fee") || !strings.Contains(result.Status, "NIR/USDT") {
		t.Fatalf("expected transparent price source, got %q", result.Status)
	}
}

func TestUnseenListingCandidatesUsesLatestSavedTopic(t *testing.T) {
	candidates := []listingCandidate{
		{URL: "https://bitcointalk.org/index.php?topic=101.0", Topic: 101},
		{URL: "https://bitcointalk.org/index.php?topic=99.0", Topic: 99},
	}
	saved := []newListing{
		{Thread: "https://bitcointalk.org/index.php?topic=100.0"},
		{Thread: "https://bitcointalk.org/index.php?topic=95.0"},
	}
	unseen := unseenListingCandidates(candidates, saved)
	if len(unseen) != 1 || unseen[0].Topic != 101 {
		t.Fatalf("unexpected unseen candidates %+v", unseen)
	}
}

func TestParseWhatToMineCoinsSkipsNicehashRentalRows(t *testing.T) {
	body := `{"coins":{"Nicehash-RandomX":{"tag":"NICEHASH","algorithm":"RandomX","block_time":"1","block_reward":1,"nethash":108077415,"exchange_rate":0.0003},` +
		`"Monero":{"tag":"XMR","algorithm":"RandomX","block_time":"120","block_reward":0.6,"nethash":6064254294,"exchange_rate":0.005}}}`
	coins, err := parseWhatToMineCoins(body, 100000)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(coins) != 1 || coins[0].Tag != "XMR" {
		t.Fatalf("expected only Monero, got %+v", coins)
	}
}

func TestNewMiningPoolStatsCalcCoinsDropsImpossibleNetworkHashrate(t *testing.T) {
	rows := []mpsRow{
		{Name: "Veil", Symbol: "VEIL", Algo: "RandomX", Hashrate: 0.000013844, Emissions24: 14230, Price: 0.00115, BlockTime: 60.7},
		{Name: "Veil", Symbol: "VEIL", Algo: "ProgPow", Hashrate: 207067470, Emissions24: 14230, Price: 0.00115, BlockTime: 60.7},
	}
	coins := newMiningPoolStatsCalcCoins(rows, map[string]bool{})
	if len(coins) != 1 {
		t.Fatalf("expected the stale RandomX page to be dropped, got %d coins", len(coins))
	}
	if coins[0].Algorithm != "ProgPow" {
		t.Fatalf("unexpected coin %+v", coins[0])
	}
}

func TestNetworkCoinsPerDayGuardsInvalidInput(t *testing.T) {
	if networkCoinsPerDay(0, 10) != 0 || networkCoinsPerDay(60, 0) != 0 {
		t.Fatal("expected invalid block data to yield no emission")
	}
	if networkCoinsPerDay(600, 3.125) != 450 {
		t.Fatalf("unexpected emission %v", networkCoinsPerDay(600, 3.125))
	}
}

func TestParseWhatToMineHardwareCatalogs(t *testing.T) {
	gpus := parseWhatToMineGPUList(`<a class="manufacturer--nvidia stretched-link" href="/gpus/92-nvidia-geforce-rtx-5090?cost=0.1">GeForce RTX 5090</a>`)
	if len(gpus) != 1 || gpus[0].ID != "92" || gpus[0].Vendor != "NVIDIA" || gpus[0].Name != "GeForce RTX 5090" {
		t.Fatalf("unexpected GPUs: %+v", gpus)
	}
	asics := parseWhatToMineASICList(`id="table_row_asic_304"><td><a class="stretched-link" href="/asics/304-bitmain-antminer">Bitmain Antminer S21E XP</a></td><td class="text-end table-">860.00 Th/s @ 11180W<div>SHA-256</div></td>`)
	if len(asics) != 1 || asics[0].ID != "304" || asics[0].Benchmarks[0].Hashrate != 860e12 || asics[0].Benchmarks[0].Watts != 11180 {
		t.Fatalf("unexpected ASICs: %+v", asics)
	}
}

func TestParseHardwareBenchmarks(t *testing.T) {
	gpuBody := `<a href="/coins/1-rvn/gpus/92">Ravencoin (RVN)</a><br>KawPow<span class="text-body-secondary fs-6">85.00 Mh/s @ 350W</span>`
	gpu := parseWhatToMineGPUBenchmarks(gpuBody)
	if len(gpu) != 1 || gpu[0].Algorithm != "KawPow" || gpu[0].Hashrate != 85e6 || gpu[0].Watts != 350 {
		t.Fatalf("unexpected GPU benchmarks: %+v", gpu)
	}
	cpuBody := `<li><a href='/cpus/9950x3d/XMR'><div><div class='name'>Monero</div><div class='estimatesDescription'>RandomX</div><div class='estimates'>20.75 Kh/s</div><div class='estimatesDescription'>Hashrate XMR</div><div class='estimatesDescription'>Power</div><div class='estimates'>170 w</div></div></a></li>`
	cpu := parseHashrateCPUBenchmarks(cpuBody)
	if len(cpu) != 1 || cpu[0].Algorithm != "RandomX" || cpu[0].Hashrate != 20750 || cpu[0].Watts != 170 {
		t.Fatalf("unexpected CPU benchmarks: %+v", cpu)
	}
}

func TestParseHashrateCPUList(t *testing.T) {
	body := `<li><div onclick="window.location.href='/cpus/9950x3d'"><span class='hidden-search'>AMD Ryzen 9 9950X3D</span><div class='brand'>AMD</div><div class='model' style='color:red'>Ryzen 9 9950X3D</div></div></li>`
	cpus := parseHashrateCPUList(body)
	if len(cpus) != 1 || cpus[0].ID != "9950x3d" || cpus[0].Vendor != "AMD" || cpus[0].Name != "Ryzen 9 9950X3D" {
		t.Fatalf("unexpected CPUs: %+v", cpus)
	}
}

func TestAnnotateCalcCoinPagesMatchesSymbolAndAlgorithm(t *testing.T) {
	coins := []calcCoin{{Tag: "RVN", Algorithm: "KawPow"}, {Tag: "XMR", Algorithm: "RandomX"}}
	rows := []mpsRow{{Symbol: "RVN", Algo: "KawPow", Page: "ravencoin"}, {Symbol: "XMR", Algo: "RandomX", Page: "monero"}}
	annotateCalcCoinPages(coins, rows)
	if coins[0].PageURL != "https://miningpoolstats.stream/ravencoin" || coins[1].PageURL != "https://miningpoolstats.stream/monero" {
		t.Fatalf("unexpected pool URLs: %+v", coins)
	}
}

func TestEnumerateHostsExcludesNetworkAndBroadcast(t *testing.T) {
	hosts, network, err := enumerateHosts("192.168.1.0/30")
	if err != nil {
		t.Fatalf("enumerate hosts: %v", err)
	}
	if network != "192.168.1.0/30" {
		t.Fatalf("unexpected network %q", network)
	}
	if len(hosts) != 2 || hosts[0] != "192.168.1.1" || hosts[1] != "192.168.1.2" {
		t.Fatalf("unexpected hosts %v", hosts)
	}
}

func TestEnumerateHostsNormalisesHostAddressAndRejectsLargeRanges(t *testing.T) {
	hosts, network, err := enumerateHosts("192.168.1.57/24")
	if err != nil {
		t.Fatalf("enumerate hosts: %v", err)
	}
	if network != "192.168.1.0/24" || len(hosts) != 254 {
		t.Fatalf("unexpected network %q with %d hosts", network, len(hosts))
	}
	if _, _, err := enumerateHosts("10.0.0.0/8"); err == nil {
		t.Fatal("expected oversized networks to be rejected")
	}
	if _, _, err := enumerateHosts("not-a-network"); err == nil {
		t.Fatal("expected invalid input to be rejected")
	}
}

func TestFingerprintDeviceIdentifiesHiveOS(t *testing.T) {
	pages := map[int]scanPage{
		80:   {Port: 80, Scheme: "http", Body: "welcome to hive os dashboard"},
		4200: {Port: 4200, Scheme: "http", Body: "shell in a box"},
	}
	detection := fingerprintDevice("rig-01", []int{80, 4200}, pages, "")
	if detection.Platform != "HiveOS" {
		t.Fatalf("expected HiveOS, got %q", detection.Platform)
	}
	if detection.Confidence != "confirmed" {
		t.Fatalf("expected a confirmed match, got %q with score %d", detection.Confidence, detection.Score)
	}
	if detection.Score > 100 {
		t.Fatalf("expected the score to be capped at 100, got %d", detection.Score)
	}
}

func TestFingerprintDeviceCapturesJasminerModel(t *testing.T) {
	pages := map[int]scanPage{80: {Port: 80, Scheme: "http", Body: "jasminer x4-q status page"}}
	detection := fingerprintDevice("", []int{80}, pages, "")
	if detection.Platform != "Jasminer" {
		t.Fatalf("expected Jasminer, got %q", detection.Platform)
	}
	if detection.Model != "X4-Q" {
		t.Fatalf("expected the model to be captured, got %q", detection.Model)
	}
	if detection.MatchedPort != 80 {
		t.Fatalf("expected the matching port to be recorded, got %d", detection.MatchedPort)
	}
}

func TestFingerprintDevicePrefersSpecificPlatformOverGenericRig(t *testing.T) {
	pages := map[int]scanPage{80: {Port: 80, Scheme: "http", Body: "miner dash hashrate stratum"}}
	detection := fingerprintDevice("worker-7", []int{80}, pages, "")
	if detection.Platform != "Miner Dash" {
		t.Fatalf("expected the specific platform to win, got %q", detection.Platform)
	}
}

func TestFingerprintDeviceReportsUnknownWithoutEvidence(t *testing.T) {
	detection := fingerprintDevice("printer", []int{80}, map[int]scanPage{80: {Port: 80, Body: "hp laserjet"}}, "")
	if detection.Platform != "" || detection.Confidence != "unknown" {
		t.Fatalf("expected an unknown device, got %+v", detection)
	}
}

func TestFingerprintDeviceUsesMinerAPIBanner(t *testing.T) {
	detection := fingerprintDevice("", []int{4028}, map[int]scanPage{}, `{"status":[{"status":"s"}],"version":[{"cgminer":"4.11"}]}`)
	if detection.Platform != "Mining rig" {
		t.Fatalf("expected the generic rig rule to match, got %q", detection.Platform)
	}
	if detection.Score != 65 {
		t.Fatalf("expected banner plus open port scoring, got %d", detection.Score)
	}
}

func TestChooseManagementURLPrefersDetectionPortThenPreferenceOrder(t *testing.T) {
	pages := map[int]scanPage{
		80:   {Port: 80, Scheme: "http"},
		4200: {Port: 4200, Scheme: "http"},
		8443: {Port: 8443, Scheme: "https"},
	}
	if url := chooseManagementURL("10.0.0.5", []int{80, 4200, 8443}, pages, 4200); url != "http://10.0.0.5:4200/" {
		t.Fatalf("expected the matched port to win, got %q", url)
	}
	if url := chooseManagementURL("10.0.0.5", []int{80, 4200, 8443}, pages, 0); url != "http://10.0.0.5/" {
		t.Fatalf("expected port 80 to omit its default port, got %q", url)
	}
	if url := chooseManagementURL("10.0.0.5", []int{4200}, map[int]scanPage{}, 0); url != "http://10.0.0.5:4200/" {
		t.Fatalf("expected the Hive shell fallback, got %q", url)
	}
	if url := chooseManagementURL("10.0.0.5", []int{22}, map[int]scanPage{}, 0); url != "" {
		t.Fatalf("expected no management URL, got %q", url)
	}
}

func TestSortScanDevicesRanksByScoreThenAddress(t *testing.T) {
	devices := []scanDevice{
		{IP: "192.168.1.20", Score: 60},
		{IP: "192.168.1.3", Score: 60},
		{IP: "192.168.1.9", Score: 95},
	}
	sortScanDevices(devices)
	if devices[0].IP != "192.168.1.9" {
		t.Fatalf("expected the strongest match first, got %q", devices[0].IP)
	}
	if devices[1].IP != "192.168.1.3" || devices[2].IP != "192.168.1.20" {
		t.Fatalf("expected numeric address ordering, got %q then %q", devices[1].IP, devices[2].IP)
	}
}

func TestExtractHTMLTitleCollapsesMarkup(t *testing.T) {
	if title := extractHTMLTitle("<html><head><TITLE>  Jasminer\n X4   </TITLE></head>"); title != "Jasminer X4" {
		t.Fatalf("unexpected title %q", title)
	}
	if title := extractHTMLTitle("<html></html>"); title != "" {
		t.Fatalf("expected no title, got %q", title)
	}
}

func TestDiscoverCacheServesStaleValueWhenRefreshFails(t *testing.T) {
	cache := newDiscoverCache[int](time.Hour)
	value, err := cache.get(t.Context(), func(context.Context) (int, error) { return 7, nil })
	if err != nil || value != 7 {
		t.Fatalf("expected the first load to succeed, got %d / %v", value, err)
	}
	cache.expires = time.Now().Add(-time.Minute)
	value, err = cache.get(t.Context(), func(context.Context) (int, error) { return 0, errors.New("upstream down") })
	if err != nil {
		t.Fatalf("expected the stale value to be served, got %v", err)
	}
	if value != 7 {
		t.Fatalf("expected the previous value, got %d", value)
	}
}

func TestDiscoverCacheReturnsErrorWithoutPreviousValue(t *testing.T) {
	cache := newDiscoverCache[int](time.Hour)
	if _, err := cache.get(t.Context(), func(context.Context) (int, error) { return 0, errors.New("upstream down") }); err == nil {
		t.Fatal("expected an error when nothing has ever loaded")
	}
}

func TestFlexFloatToleratesStringsAndNulls(t *testing.T) {
	var payload struct {
		A flexFloat `json:"a"`
		B flexFloat `json:"b"`
		C flexFloat `json:"c"`
		D flexFloat `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"a":1.5,"b":"2.5","c":null,"d":"n/a"}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.A.float() != 1.5 || payload.B.float() != 2.5 {
		t.Fatalf("unexpected numbers %v / %v", payload.A.float(), payload.B.float())
	}
	if payload.C.float() != 0 || payload.D.float() != 0 {
		t.Fatalf("expected unusable values to become zero, got %v / %v", payload.C.float(), payload.D.float())
	}
}
