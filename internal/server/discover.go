package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// discoverBrowserAgent is required because the upstream mining directories reject
// non-browser clients outright.
const discoverBrowserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

const (
	miningPoolStatsHome     = "https://miningpoolstats.stream/"
	miningPoolStatsCalendar = "https://miningpoolstats.stream/calendar"
	miningPoolStatsData     = "https://data.miningpoolstats.stream/data/coins_data.js"
	bitcointalkBoard        = "https://bitcointalk.org/index.php?board=159.0"
	bitcointalkBoardPage2   = "https://bitcointalk.org/index.php?board=159.40"
	whatToMineGPUs          = "https://whattomine.com/gpus"
	whatToMineASICs         = "https://whattomine.com/asics"
	hashrateNoCPUs          = "https://hashrate.no/cpus"
	whatToMineCoins         = "https://whattomine.com/coins.json"
	whatToMineCPU           = "https://whattomine.com/cpu.json"
	whatToMineASIC          = "https://whattomine.com/asic.json"
	coinGeckoMarkets        = "https://api.coingecko.com/api/v3/coins/markets"
)

// flexFloat accepts numbers or numeric strings because the scraped feeds are
// inconsistent between coins.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(strings.Trim(string(data), `"`))
	if text == "" || text == "null" {
		*f = 0
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexFloat(value)
	return nil
}

func (f flexFloat) float() float64 { return float64(f) }

// discoverCache serves a cached value until it expires. When a refresh fails but
// a previous value exists the stale value is returned so a flaky upstream does
// not blank out a dashboard page.
type discoverCache[T any] struct {
	mu       sync.Mutex
	ttl      time.Duration
	value    T
	loaded   bool
	expires  time.Time
	fetchErr error
}

func newDiscoverCache[T any](ttl time.Duration) *discoverCache[T] {
	return &discoverCache[T]{ttl: ttl}
}

func (c *discoverCache[T]) get(ctx context.Context, load func(context.Context) (T, error)) (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded && time.Now().Before(c.expires) {
		return c.value, nil
	}
	value, err := load(ctx)
	if err != nil {
		c.fetchErr = err
		if c.loaded {
			return c.value, nil
		}
		var zero T
		return zero, err
	}
	c.value = value
	c.loaded = true
	c.fetchErr = nil
	c.expires = time.Now().Add(c.ttl)
	return value, nil
}

type discoverProvider struct {
	client *http.Client

	powCoins   *discoverCache[[]powCoin]
	listings   *discoverCache[[]newListing]
	calendar   *discoverCache[calendarPayload]
	calcCoins  *discoverCache[[]calcCoin]
	hardware   *discoverCache[[]calcHardware]
	mpsRows    *discoverCache[[]mpsRow]
	minerAlgos *discoverCache[minerAlgorithmCatalog]

	hardwareMu       sync.Mutex
	hardwareDetails  map[string]hardwareDetailCache
	nonKYCMu         sync.Mutex
	nonKYCPrices     map[string]nonKYCPriceCache
	listingCachePath string
	cmpCachePath     string
	cmpCacheLoaded   bool
	cmpBenchmarks    map[string][]calcBenchmark

	geckoMu   sync.Mutex
	lastGecko map[string]geckoChange
	geckoErr  error
}

func newDiscoverProvider(cacheDirs ...string) *discoverProvider {
	jar, _ := cookiejar.New(nil)
	provider := &discoverProvider{
		client:          &http.Client{Timeout: 45 * time.Second, Jar: jar},
		powCoins:        newDiscoverCache[[]powCoin](30 * time.Minute),
		listings:        newDiscoverCache[[]newListing](6 * time.Hour),
		calendar:        newDiscoverCache[calendarPayload](15 * time.Minute),
		calcCoins:       newDiscoverCache[[]calcCoin](10 * time.Minute),
		hardware:        newDiscoverCache[[]calcHardware](6 * time.Hour),
		mpsRows:         newDiscoverCache[[]mpsRow](30 * time.Minute),
		minerAlgos:      newDiscoverCache[minerAlgorithmCatalog](6 * time.Hour),
		hardwareDetails: make(map[string]hardwareDetailCache),
		nonKYCPrices:    make(map[string]nonKYCPriceCache),
		cmpBenchmarks:   make(map[string][]calcBenchmark),
	}
	if len(cacheDirs) > 0 && strings.TrimSpace(cacheDirs[0]) != "" {
		provider.listingCachePath = filepath.Join(cacheDirs[0], "discover-listings-cache.json")
		provider.cmpCachePath = filepath.Join(cacheDirs[0], "discover-cmp-benchmarks.json")
	}
	return provider
}

func (p *discoverProvider) loadCMPBenchmarks() {
	if p.cmpCacheLoaded {
		return
	}
	p.cmpCacheLoaded = true
	if p.cmpCachePath == "" {
		return
	}
	data, err := os.ReadFile(p.cmpCachePath)
	if err == nil {
		_ = json.Unmarshal(data, &p.cmpBenchmarks)
	}
}

func (p *discoverProvider) saveCMPBenchmarks() {
	if p.cmpCachePath == "" {
		return
	}
	data, err := json.MarshalIndent(p.cmpBenchmarks, "", "  ")
	if err != nil {
		return
	}
	tmp := p.cmpCachePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		_ = os.Rename(tmp, p.cmpCachePath)
	}
}
func (p *discoverProvider) fetchText(ctx context.Context, rawURL, referer string, timeout time.Duration, limit int64) (string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", discoverBrowserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("%s returned %s", rawURL, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
