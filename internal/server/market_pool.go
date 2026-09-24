package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"minerdash/internal/protocol"
)

type coinDefinition struct {
	ID   string
	Name string
	Logo string
}

var coinDefinitions = map[string]coinDefinition{
	"BTC": {ID: "bitcoin", Name: "Bitcoin", Logo: "/coins/btc.svg"},
	"ETH": {ID: "ethereum", Name: "Ethereum", Logo: "/coins/eth.svg"},
	"ETC": {ID: "ethereum-classic", Name: "Ethereum Classic", Logo: "/coins/etc.svg"},
	"RVN": {ID: "ravencoin", Name: "Ravencoin", Logo: "/coins/rvn.svg"},
	"XMR": {ID: "monero", Name: "Monero", Logo: "/coins/xmr.svg"},
	"QRL": {ID: "quantum-resistant-ledger", Name: "Quantum Resistant Ledger", Logo: "/coins/qrl.svg"},
	"ZCL": {ID: "zclassic", Name: "Zclassic", Logo: "/coins/zclassic-zcl-logo.png"},
	"VTC": {ID: "vertcoin", Name: "Vertcoin", Logo: "/coins/vertcoin-vtc-logo.png"},
	"YEC": {ID: "ycash", Name: "Ycash", Logo: "/coins/ycash-yec-logo.png"},
}

type coinMarket struct {
	Symbol    string    `json:"symbol"`
	Name      string    `json:"name"`
	USD       float64   `json:"usd"`
	Logo      string    `json:"logo"`
	UpdatedAt time.Time `json:"updated_at"`
}

type coinAsset struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
	Logo   string `json:"logo,omitempty"`
}

type cachedCoinLogo struct {
	ContentType string
	Data        []byte
}

type marketProvider struct {
	mu                 sync.Mutex
	client             *http.Client
	endpoint           string
	coinListEndpoint   string
	coinMarketEndpoint string
	cached             map[string]coinMarket
	expiresAt          time.Time
	assets             map[string]coinAsset
	assetSymbols       string
	assetsExpireAt     time.Time
	logoURLs           map[string]string
	logos              map[string]cachedCoinLogo
}

func newMarketProvider() *marketProvider {
	return &marketProvider{
		client:             &http.Client{Timeout: 15 * time.Second},
		endpoint:           "https://api.coingecko.com/api/v3/simple/price",
		coinListEndpoint:   "https://api.coingecko.com/api/v3/coins/list?include_platform=false",
		coinMarketEndpoint: "https://api.coingecko.com/api/v3/coins/markets",
		logoURLs:           make(map[string]string),
		logos:              make(map[string]cachedCoinLogo),
	}
}

func (p *marketProvider) prices(ctx context.Context) (map[string]coinMarket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.expiresAt) && len(p.cached) > 0 {
		return cloneMarkets(p.cached), nil
	}
	ids := make([]string, 0, len(coinDefinitions))
	for _, definition := range coinDefinitions {
		ids = append(ids, definition.ID)
	}
	sort.Strings(ids)
	endpoint, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("ids", strings.Join(ids, ","))
	query.Set("vs_currencies", "usd")
	query.Set("include_last_updated_at", "true")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MinerDash/0.1")
	var response map[string]struct {
		USD           float64 `json:"usd"`
		LastUpdatedAt int64   `json:"last_updated_at"`
	}
	if err := getJSON(p.client, request, &response); err != nil {
		return nil, fmt.Errorf("load CoinGecko prices: %w", err)
	}
	result := make(map[string]coinMarket)
	for symbol, definition := range coinDefinitions {
		value, ok := response[definition.ID]
		if !ok || value.USD <= 0 {
			continue
		}
		result[symbol] = coinMarket{
			Symbol: symbol, Name: definition.Name, USD: value.USD, Logo: definition.Logo,
			UpdatedAt: time.Unix(value.LastUpdatedAt, 0).UTC(),
		}
	}
	p.cached = result
	p.expiresAt = time.Now().Add(5 * time.Minute)
	return cloneMarkets(result), nil
}

func cloneMarkets(source map[string]coinMarket) map[string]coinMarket {
	result := make(map[string]coinMarket, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (p *marketProvider) coinAssets(ctx context.Context, symbols []string) (map[string]coinAsset, error) {
	normalized := make([]string, 0, len(symbols))
	seen := make(map[string]bool)
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol != "" && !seen[symbol] {
			seen[symbol] = true
			normalized = append(normalized, symbol)
		}
	}
	sort.Strings(normalized)
	signature := strings.Join(normalized, ",")

	p.mu.Lock()
	defer p.mu.Unlock()
	if signature == p.assetSymbols && time.Now().Before(p.assetsExpireAt) {
		return cloneCoinAssets(p.assets), nil
	}

	result := make(map[string]coinAsset, len(normalized))
	unresolved := make(map[string]bool)
	for _, symbol := range normalized {
		if definition, ok := coinDefinitions[symbol]; ok {
			result[symbol] = coinAsset{Symbol: symbol, Name: definition.Name, Logo: definition.Logo}
		} else {
			result[symbol] = coinAsset{Symbol: symbol, Name: symbol}
			unresolved[symbol] = true
		}
	}
	if len(unresolved) == 0 {
		p.assets, p.assetSymbols, p.assetsExpireAt = result, signature, time.Now().Add(24*time.Hour)
		return cloneCoinAssets(result), nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.coinListEndpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MinerDash/0.1")
	var listed []struct {
		ID     string `json:"id"`
		Symbol string `json:"symbol"`
		Name   string `json:"name"`
	}
	if err := getJSON(p.client, request, &listed); err != nil {
		return nil, fmt.Errorf("load CoinGecko coin list: %w", err)
	}
	candidates := make(map[string][]coinAsset)
	for _, coin := range listed {
		symbol := strings.ToUpper(coin.Symbol)
		if unresolved[symbol] {
			candidates[symbol] = append(candidates[symbol], coinAsset{Symbol: symbol, Name: coin.Name, Logo: coin.ID})
		}
	}
	ids := make([]string, 0, len(unresolved))
	idSymbols := make(map[string]string)
	for symbol := range unresolved {
		options := candidates[symbol]
		var selected coinAsset
		if len(options) == 1 {
			selected = options[0]
		} else {
			for _, option := range options {
				if strings.EqualFold(option.Logo, symbol) {
					selected = option
					break
				}
			}
		}
		if selected.Logo == "" {
			continue
		}
		ids = append(ids, selected.Logo)
		idSymbols[selected.Logo] = symbol
		result[symbol] = coinAsset{Symbol: symbol, Name: selected.Name}
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		endpoint, err := url.Parse(p.coinMarketEndpoint)
		if err != nil {
			return nil, err
		}
		query := endpoint.Query()
		query.Set("vs_currency", "usd")
		query.Set("ids", strings.Join(ids[start:end], ","))
		query.Set("per_page", "100")
		query.Set("sparkline", "false")
		endpoint.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "MinerDash/0.1")
		var markets []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Image string `json:"image"`
		}
		if err := getJSON(p.client, request, &markets); err != nil {
			return nil, fmt.Errorf("load CoinGecko coin assets: %w", err)
		}
		for _, market := range markets {
			symbol := idSymbols[market.ID]
			if symbol == "" || !validCoinGeckoImageURL(market.Image) {
				continue
			}
			result[symbol] = coinAsset{Symbol: symbol, Name: market.Name, Logo: "/coin-logo/" + url.PathEscape(symbol)}
			p.logoURLs[symbol] = market.Image
		}
	}
	p.assets, p.assetSymbols, p.assetsExpireAt = result, signature, time.Now().Add(24*time.Hour)
	return cloneCoinAssets(result), nil
}

func (p *marketProvider) coinLogo(ctx context.Context, symbol string) (cachedCoinLogo, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	p.mu.Lock()
	if logo, ok := p.logos[symbol]; ok {
		p.mu.Unlock()
		return logo, nil
	}
	logoURL := p.logoURLs[symbol]
	p.mu.Unlock()
	if !validCoinGeckoImageURL(logoURL) {
		return cachedCoinLogo{}, ErrNotFound
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, logoURL, nil)
	if err != nil {
		return cachedCoinLogo{}, err
	}
	request.Header.Set("User-Agent", "MinerDash/0.1")
	response, err := p.client.Do(request)
	if err != nil {
		return cachedCoinLogo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return cachedCoinLogo{}, fmt.Errorf("CoinGecko logo returned HTTP %d", response.StatusCode)
	}
	contentType := response.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return cachedCoinLogo{}, errors.New("CoinGecko logo response is not an image")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return cachedCoinLogo{}, err
	}
	if len(data) == 0 || len(data) >= 2<<20 {
		return cachedCoinLogo{}, errors.New("CoinGecko logo response has an invalid size")
	}
	logo := cachedCoinLogo{ContentType: contentType, Data: data}
	p.mu.Lock()
	p.logos[symbol] = logo
	p.mu.Unlock()
	return logo, nil
}

func validCoinGeckoImageURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "coin-images.coingecko.com" || host == "assets.coingecko.com"
}

func cloneCoinAssets(source map[string]coinAsset) map[string]coinAsset {
	result := make(map[string]coinAsset, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type poolStatsProvider struct {
	mu     sync.Mutex
	client *http.Client
	cache  map[string]poolSnapshot
}

type poolSnapshot struct {
	Blocks    []poolBlock
	Payments  []poolPayment
	Workers   map[string]string
	ExpiresAt time.Time
}

type poolBlock struct {
	Height  int64     `json:"blockHeight"`
	Reward  float64   `json:"reward"`
	Miner   string    `json:"miner"`
	Created time.Time `json:"created"`
}

type poolPayment struct {
	Address string    `json:"address"`
	Amount  float64   `json:"amount"`
	Created time.Time `json:"created"`
}

type poolPeriod struct {
	Blocks     int     `json:"blocks"`
	MinedCoins float64 `json:"mined_coins"`
	PaidCoins  float64 `json:"paid_coins"`
	USDValue   float64 `json:"usd_value"`
}

type workerPoolPerformance struct {
	Available bool                  `json:"available"`
	Message   string                `json:"message,omitempty"`
	Coin      string                `json:"coin,omitempty"`
	Scope     string                `json:"scope,omitempty"`
	PriceUSD  float64               `json:"price_usd,omitempty"`
	Periods   map[string]poolPeriod `json:"periods,omitempty"`
}

func newPoolStatsProvider() *poolStatsProvider {
	return &poolStatsProvider{
		client: &http.Client{Timeout: 12 * time.Second},
		cache:  make(map[string]poolSnapshot),
	}
}

func (p *poolStatsProvider) performance(ctx context.Context, rig protocol.Rig, pool protocol.Pool, wallet protocol.Wallet, price float64) (workerPoolPerformance, error) {
	if pool.StatsURL == "" || pool.StatsPoolID == "" {
		return workerPoolPerformance{Available: false, Message: "Pool statistics API is not configured for this pool."}, nil
	}
	snapshot, err := p.snapshot(ctx, pool)
	if err != nil {
		return workerPoolPerformance{}, err
	}
	blocks := make([]poolBlock, 0)
	attributed := false
	for _, block := range snapshot.Blocks {
		if !strings.EqualFold(block.Miner, wallet.Address) {
			continue
		}
		worker := snapshot.Workers[fmt.Sprint(block.Height)]
		if worker != "" {
			attributed = true
			if !strings.EqualFold(worker, rig.Name) {
				continue
			}
		}
		blocks = append(blocks, block)
	}
	payments := make([]poolPayment, 0)
	for _, payment := range snapshot.Payments {
		if strings.EqualFold(payment.Address, wallet.Address) {
			payments = append(payments, payment)
		}
	}
	now := time.Now().UTC()
	periods := map[string]poolPeriod{
		"24h": summarizePoolPeriod(blocks, payments, now.Add(-24*time.Hour), price),
		"7d":  summarizePoolPeriod(blocks, payments, now.Add(-7*24*time.Hour), price),
		"all": summarizePoolPeriod(blocks, payments, time.Time{}, price),
	}
	scope := "wallet"
	if attributed {
		scope = "worker"
	}
	return workerPoolPerformance{
		Available: true, Coin: wallet.Coin, Scope: scope, PriceUSD: price, Periods: periods,
	}, nil
}

func summarizePoolPeriod(blocks []poolBlock, payments []poolPayment, since time.Time, price float64) poolPeriod {
	var result poolPeriod
	for _, block := range blocks {
		if since.IsZero() || !block.Created.Before(since) {
			result.Blocks++
			result.MinedCoins += block.Reward
		}
	}
	for _, payment := range payments {
		if since.IsZero() || !payment.Created.Before(since) {
			result.PaidCoins += payment.Amount
		}
	}
	result.USDValue = result.MinedCoins * price
	return result
}

func (p *poolStatsProvider) snapshot(ctx context.Context, pool protocol.Pool) (poolSnapshot, error) {
	key := pool.StatsURL + "|" + pool.StatsPoolID
	p.mu.Lock()
	defer p.mu.Unlock()
	if cached, ok := p.cache[key]; ok && time.Now().Before(cached.ExpiresAt) {
		return cached, nil
	}
	base := strings.TrimRight(pool.StatsURL, "/")
	var blocks []poolBlock
	if err := requestJSON(ctx, p.client, http.MethodGet, fmt.Sprintf("%s/api/pools/%s/blocks?page=0&pageSize=5000", base, url.PathEscape(pool.StatsPoolID)), nil, &blocks); err != nil {
		return poolSnapshot{}, fmt.Errorf("load pool blocks: %w", err)
	}
	var payments []poolPayment
	if err := requestJSON(ctx, p.client, http.MethodGet, fmt.Sprintf("%s/api/pools/%s/payments?page=0&pageSize=5000", base, url.PathEscape(pool.StatsPoolID)), nil, &payments); err != nil {
		return poolSnapshot{}, fmt.Errorf("load pool payments: %w", err)
	}
	heights := make([]int64, 0, len(blocks))
	for _, block := range blocks {
		heights = append(heights, block.Height)
	}
	workers := make(map[string]string)
	if len(heights) > 0 {
		body, err := json.Marshal(map[string]any{"poolId": pool.StatsPoolID, "heights": heights})
		if err != nil {
			return poolSnapshot{}, err
		}
		if err := requestJSON(ctx, p.client, http.MethodPost, base+"/dashboard/block-workers", bytes.NewReader(body), &workers); err != nil {
			return poolSnapshot{}, fmt.Errorf("load block worker attribution: %w", err)
		}
	}
	result := poolSnapshot{Blocks: blocks, Payments: payments, Workers: workers, ExpiresAt: time.Now().Add(2 * time.Minute)}
	p.cache[key] = result
	return result, nil
}

func requestJSON(ctx context.Context, client *http.Client, method, endpoint string, body io.Reader, target any) error {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MinerDash/0.1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return getJSON(client, request, target)
}

func getJSON(client *http.Client, request *http.Request, target any) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("upstream returned %s", response.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 32<<20))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func (s *HTTPServer) coinPrices(w http.ResponseWriter, r *http.Request) {
	prices, err := s.market.prices(r.Context())
	if err != nil {
		s.logger.Printf("coin prices: %v", err)
		http.Error(w, "coin prices are temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, prices)
}

func (s *HTTPServer) coinAssets(w http.ResponseWriter, r *http.Request) {
	wallets := s.store.ListWallets()
	symbols := make([]string, 0, len(wallets))
	for _, wallet := range wallets {
		symbols = append(symbols, wallet.Coin)
	}
	assets, err := s.market.coinAssets(r.Context(), symbols)
	if err != nil {
		s.logger.Printf("coin assets: %v", err)
		http.Error(w, "coin logos are temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, assets)
}

func (s *HTTPServer) coinLogo(w http.ResponseWriter, r *http.Request) {
	symbol := strings.ToUpper(strings.TrimSpace(r.PathValue("symbol")))
	if symbol == "" || len(symbol) > 32 || strings.IndexFunc(symbol, func(character rune) bool {
		return (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' && character != '.'
	}) >= 0 {
		http.NotFound(w, r)
		return
	}
	logo, err := s.market.coinLogo(r.Context(), symbol)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.logger.Printf("coin logo %s: %v", symbol, err)
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", logo.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Data); err != nil {
		s.logger.Printf("coin logo %s response: %v", symbol, err)
	}
}

func (s *HTTPServer) workerPoolStats(w http.ResponseWriter, r *http.Request) {
	rig, pool, wallet, err := s.store.WorkerPoolContext(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	prices, err := s.market.prices(r.Context())
	if err != nil {
		s.logger.Printf("pool stats coin price: %v", err)
		http.Error(w, "coin price is temporarily unavailable", http.StatusBadGateway)
		return
	}
	price := prices[strings.ToUpper(wallet.Coin)].USD
	result, err := s.poolStats.performance(r.Context(), rig, pool, wallet, price)
	if err != nil {
		s.logger.Printf("pool stats for rig %s: %v", rig.ID, err)
		http.Error(w, "pool statistics are temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Store) WorkerPoolContext(rigID string) (protocol.Rig, protocol.Pool, protocol.Wallet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return protocol.Rig{}, protocol.Pool{}, protocol.Wallet{}, ErrNotFound
	}
	sheet, ok := s.state.FlightSheets[rig.Desired.FlightSheetID]
	if !ok {
		return protocol.Rig{}, protocol.Pool{}, protocol.Wallet{}, errors.New("worker has no flight sheet")
	}
	pool, ok := s.state.Pools[sheet.PoolID]
	if !ok {
		return protocol.Rig{}, protocol.Pool{}, protocol.Wallet{}, errors.New("worker pool does not exist")
	}
	wallet, ok := s.state.Wallets[sheet.WalletID]
	if !ok {
		return protocol.Rig{}, protocol.Pool{}, protocol.Wallet{}, errors.New("worker wallet does not exist")
	}
	return rig.Rig, pool, wallet, nil
}
