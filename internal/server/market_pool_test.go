package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"minerdash/internal/protocol"
)

func TestMarketProviderCachesCoinGeckoPrices(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("vs_currencies") != "usd" {
			t.Errorf("vs_currencies = %q", r.URL.Query().Get("vs_currencies"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"zclassic":                 map[string]any{"usd": 0.93, "last_updated_at": 1_790_000_000},
			"quantum-resistant-ledger": map[string]any{"usd": 0.67, "last_updated_at": 1_790_000_001},
		})
	}))
	defer server.Close()

	provider := newMarketProvider()
	provider.endpoint = server.URL
	first, err := provider.prices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.prices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first["ZCL"].USD != 0.93 || second["QRL"].USD != 0.67 {
		t.Fatalf("prices = %#v", first)
	}
	if requests.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1", requests.Load())
	}
}

func TestMarketProviderResolvesUnambiguousCoinLogos(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/list":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"id": "alpha-coin", "symbol": "abc", "name": "Alpha Coin"},
				{"id": "duplicate-one", "symbol": "dup", "name": "Duplicate One"},
				{"id": "duplicate-two", "symbol": "dup", "name": "Duplicate Two"},
			})
		case "/markets":
			if r.URL.Query().Get("ids") != "alpha-coin" {
				t.Errorf("ids = %q", r.URL.Query().Get("ids"))
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{
				"id": "alpha-coin", "name": "Alpha Coin",
				"image": "https://coin-images.coingecko.com/coins/images/1/large/alpha.png",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newMarketProvider()
	provider.coinListEndpoint = server.URL + "/list"
	provider.coinMarketEndpoint = server.URL + "/markets"
	first, err := provider.coinAssets(t.Context(), []string{"ABC", "DUP", "BTC"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.coinAssets(t.Context(), []string{"BTC", "DUP", "ABC"})
	if err != nil {
		t.Fatal(err)
	}
	if first["ABC"].Logo != "/coin-logo/ABC" || first["DUP"].Logo != "" || first["BTC"].Logo != "/coins/btc.svg" {
		t.Fatalf("assets = %#v", first)
	}
	if second["ABC"].Name != "Alpha Coin" || requests.Load() != 2 {
		t.Fatalf("cached assets = %#v, requests = %d", second, requests.Load())
	}
}

func TestPoolPerformanceWindowsAndWorkerAttribution(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pools/zcl/blocks":
			_ = json.NewEncoder(w).Encode([]poolBlock{
				{Height: 1, Reward: 0.4, Miner: "wallet", Created: now.Add(-time.Hour)},
				{Height: 2, Reward: 0.4, Miner: "wallet", Created: now.Add(-48 * time.Hour)},
				{Height: 3, Reward: 0.4, Miner: "wallet", Created: now.Add(-8 * 24 * time.Hour)},
				{Height: 4, Reward: 9, Miner: "other", Created: now.Add(-time.Hour)},
			})
		case "/api/pools/zcl/payments":
			_ = json.NewEncoder(w).Encode([]poolPayment{
				{Address: "wallet", Amount: 1, Created: now.Add(-time.Hour)},
				{Address: "wallet", Amount: 2, Created: now.Add(-10 * 24 * time.Hour)},
				{Address: "other", Amount: 9, Created: now.Add(-time.Hour)},
			})
		case "/dashboard/block-workers":
			_ = json.NewEncoder(w).Encode(map[string]string{"1": "GPU-1", "2": "GPU-1", "3": "GPU-1", "4": "GPU-2"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newPoolStatsProvider()
	result, err := provider.performance(t.Context(),
		protocol.Rig{Name: "GPU-1"},
		protocol.Pool{StatsURL: server.URL, StatsPoolID: "zcl"},
		protocol.Wallet{Coin: "ZCL", Address: "wallet"},
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || result.Scope != "worker" {
		t.Fatalf("result = %#v", result)
	}
	if result.Periods["24h"].Blocks != 1 || result.Periods["24h"].PaidCoins != 1 || result.Periods["24h"].USDValue != 0.8 {
		t.Fatalf("24h = %#v", result.Periods["24h"])
	}
	if result.Periods["7d"].Blocks != 2 || result.Periods["all"].Blocks != 3 || result.Periods["all"].PaidCoins != 3 {
		t.Fatalf("periods = %#v", result.Periods)
	}
}
