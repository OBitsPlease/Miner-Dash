package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type mpsRow struct {
	Name         string      `json:"name"`
	Symbol       string      `json:"symbol"`
	Algo         string      `json:"algo"`
	Page         string      `json:"page"`
	Type         string      `json:"typ"`
	Unit         string      `json:"unit"`
	Pools        int         `json:"pools"`
	Price        flexFloat   `json:"pr"`
	MarketCap    flexFloat   `json:"mc"`
	Volume24     flexFloat   `json:"v24"`
	Change7d     flexFloat   `json:"c7d"`
	Emissions24  flexFloat   `json:"e24"`
	BlockTime    flexFloat   `json:"bt"`
	Target       flexFloat   `json:"target"`
	Difficulty   flexFloat   `json:"diff"`
	Difficulty7d []flexFloat `json:"diff7"`
	Hashrate     flexFloat   `json:"hashrate"`
	PoolHashrate flexFloat   `json:"ph"`
	Age          flexFloat   `json:"age"`
}

type powCoin struct {
	Name            string   `json:"name"`
	Symbol          string   `json:"symbol"`
	Algo            string   `json:"algo"`
	Page            string   `json:"page"`
	PageURL         string   `json:"page_url"`
	Type            string   `json:"type"`
	Unit            string   `json:"unit"`
	Pools           int      `json:"pools"`
	Price           float64  `json:"price"`
	Change1h        *float64 `json:"change_1h"`
	Change24h       *float64 `json:"change_24h"`
	Change7d        *float64 `json:"change_7d"`
	MarketCap       float64  `json:"market_cap"`
	Volume24h       float64  `json:"volume_24h"`
	NetworkHashrate float64  `json:"network_hashrate"`
	PoolHashrate    float64  `json:"pool_hashrate"`
	Difficulty      float64  `json:"difficulty"`
	Diff7dChange    *float64 `json:"diff_7d_change"`
	Emissions24h    float64  `json:"emissions_24h"`
	BlockTime       float64  `json:"block_time"`
}

var coinsDataPattern = regexp.MustCompile(`coins_data\.js\?t=(\d+)`)

// difficultySevenDaysAgo reads the oldest sample of the rolling difficulty
// history the directory publishes alongside the current value.
func (r mpsRow) difficultySevenDaysAgo() float64 {
	for _, sample := range r.Difficulty7d {
		if value := sample.float(); value > 0 {
			return value
		}
	}
	return 0
}

// miningPoolStatsRows performs the two-step fetch the upstream CDN requires: the
// homepage establishes a session and advertises the current data timestamp, and
// only then will the data host serve the coin feed.
func (p *discoverProvider) miningPoolStatsRows(ctx context.Context) ([]mpsRow, error) {
	return p.mpsRows.get(ctx, func(ctx context.Context) ([]mpsRow, error) {
		home, err := p.fetchText(ctx, miningPoolStatsHome, "", 25*time.Second, 8<<20)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		if err == nil {
			if match := coinsDataPattern.FindStringSubmatch(home); len(match) == 2 {
				stamp = match[1]
			}
		}
		body, err := p.fetchText(ctx, miningPoolStatsData+"?t="+stamp, miningPoolStatsHome, 30*time.Second, 32<<20)
		if err != nil {
			return nil, fmt.Errorf("load MiningPoolStats coins: %w", err)
		}
		return parseMiningPoolStatsRows(body)
	})
}

func parseMiningPoolStatsRows(body string) ([]mpsRow, error) {
	start := strings.Index(body, "{")
	if start < 0 {
		return nil, fmt.Errorf("MiningPoolStats feed is not JSON")
	}
	var payload struct {
		Data []mpsRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(body[start:]), &payload); err != nil {
		return nil, fmt.Errorf("decode MiningPoolStats feed: %w", err)
	}
	rows := make([]mpsRow, 0, len(payload.Data))
	for _, row := range payload.Data {
		if strings.TrimSpace(row.Name) == "" {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("MiningPoolStats feed contained no coins")
	}
	return rows, nil
}

type geckoChange struct {
	Change1h  *float64
	Change24h *float64
}

func (p *discoverProvider) coinGeckoChanges(ctx context.Context) map[string]geckoChange {
	changes, err := p.fetchCoinGeckoChanges(ctx)
	p.geckoMu.Lock()
	defer p.geckoMu.Unlock()
	p.geckoErr = err
	// CoinGecko rate-limits aggressively, so the last good response is reused
	// rather than blanking the short-term price columns.
	if len(changes) == 0 {
		return p.lastGecko
	}
	p.lastGecko = changes
	return changes
}

// coinGeckoStatus reports the most recent short-term price refresh failure.
func (p *discoverProvider) coinGeckoStatus() error {
	p.geckoMu.Lock()
	defer p.geckoMu.Unlock()
	return p.geckoErr
}

func (p *discoverProvider) fetchCoinGeckoChanges(ctx context.Context) (map[string]geckoChange, error) {
	changes := make(map[string]geckoChange)
	for page := 1; page <= 2; page++ {
		endpoint, err := url.Parse(coinGeckoMarkets)
		if err != nil {
			return changes, err
		}
		query := endpoint.Query()
		query.Set("vs_currency", "usd")
		query.Set("order", "market_cap_desc")
		query.Set("per_page", "250")
		query.Set("page", strconv.Itoa(page))
		query.Set("price_change_percentage", "1h,24h,7d")
		endpoint.RawQuery = query.Encode()
		body, err := p.fetchText(ctx, endpoint.String(), "", 20*time.Second, 16<<20)
		if err != nil {
			return changes, err
		}
		var markets []struct {
			Symbol string   `json:"symbol"`
			Hour   *float64 `json:"price_change_percentage_1h_in_currency"`
			Day    *float64 `json:"price_change_percentage_24h_in_currency"`
		}
		if err := json.Unmarshal([]byte(body), &markets); err != nil {
			return changes, fmt.Errorf("decode CoinGecko markets: %w", err)
		}
		for _, market := range markets {
			symbol := strings.ToUpper(strings.TrimSpace(market.Symbol))
			if symbol == "" {
				continue
			}
			if _, exists := changes[symbol]; exists {
				continue
			}
			changes[symbol] = geckoChange{Change1h: market.Hour, Change24h: market.Day}
		}
	}
	return changes, nil
}

func (p *discoverProvider) powPrices(ctx context.Context) ([]powCoin, error) {
	return p.powCoins.get(ctx, func(ctx context.Context) ([]powCoin, error) {
		rows, err := p.miningPoolStatsRows(ctx)
		if err != nil {
			return nil, err
		}
		changes := p.coinGeckoChanges(ctx)
		return buildPOWCoins(rows, changes), nil
	})
}

func buildPOWCoins(rows []mpsRow, changes map[string]geckoChange) []powCoin {
	coins := make([]powCoin, 0, len(rows))
	for _, row := range rows {
		coin := powCoin{
			Name:            strings.TrimSpace(row.Name),
			Symbol:          strings.ToUpper(strings.TrimSpace(row.Symbol)),
			Algo:            strings.TrimSpace(row.Algo),
			Page:            strings.TrimSpace(row.Page),
			Type:            strings.TrimSpace(row.Type),
			Unit:            strings.TrimSpace(row.Unit),
			Pools:           row.Pools,
			Price:           row.Price.float(),
			MarketCap:       row.MarketCap.float(),
			Volume24h:       row.Volume24.float(),
			NetworkHashrate: row.Hashrate.float(),
			PoolHashrate:    row.PoolHashrate.float(),
			Difficulty:      row.Difficulty.float(),
			Emissions24h:    row.Emissions24.float(),
			BlockTime:       row.BlockTime.float(),
		}
		if coin.Page != "" {
			coin.PageURL = miningPoolStatsHome + coin.Page
		}
		if value := row.Change7d.float(); value != 0 {
			change := value
			coin.Change7d = &change
		}
		if current, previous := row.Difficulty.float(), row.difficultySevenDaysAgo(); current > 0 && previous > 0 {
			change := (current - previous) / previous * 100
			coin.Diff7dChange = &change
		}
		if market, ok := changes[coin.Symbol]; ok {
			coin.Change1h = market.Change1h
			coin.Change24h = market.Change24h
		}
		coins = append(coins, coin)
	}
	sort.SliceStable(coins, func(i, j int) bool { return coins[i].MarketCap > coins[j].MarketCap })
	return coins
}
