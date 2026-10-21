package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// minCalcNetworkHashrate is the lowest network hashrate the calculator will
// accept from a scraped feed. Anything below one hash per second is stale or
// broken upstream data rather than a real network.
const minCalcNetworkHashrate = 1.0

type calcCoin struct {
	Name            string  `json:"name"`
	Tag             string  `json:"tag"`
	Algorithm       string  `json:"algorithm"`
	Unit            string  `json:"unit"`
	BlockTime       float64 `json:"block_time"`
	BlockReward     float64 `json:"block_reward"`
	NetworkHashrate float64 `json:"network_hashrate"`
	Difficulty      float64 `json:"difficulty"`
	PriceUSD        float64 `json:"price_usd"`
	CoinsPerDay     float64 `json:"network_coins_per_day"`
	Source          string  `json:"source"`
	IsNew           bool    `json:"is_new"`
	PageURL         string  `json:"page_url"`
	RecentlyIndexed bool    `json:"-"`
}

type whatToMineCoin struct {
	ID            int       `json:"id"`
	Tag           string    `json:"tag"`
	Algorithm     string    `json:"algorithm"`
	BlockTime     flexFloat `json:"block_time"`
	BlockReward   flexFloat `json:"block_reward"`
	BlockReward24 flexFloat `json:"block_reward24"`
	Difficulty    flexFloat `json:"difficulty"`
	NetHash       flexFloat `json:"nethash"`
	ExchangeRate  flexFloat `json:"exchange_rate"`
	Lagging       bool      `json:"lagging"`
}

// solAlgorithms report network hashrate in solutions per second rather than
// hashes per second, which changes the unit the calculator must display.
var solAlgorithms = map[string]bool{
	"Equihash":        true,
	"Equihash(144,5)": true,
	"Equihash(192,7)": true,
	"Equihash(125,4)": true,
	"BeamHashIII":     true,
	"ZHash":           true,
}

func calcUnitForAlgorithm(algorithm string) string {
	if solAlgorithms[strings.TrimSpace(algorithm)] {
		return "Sol/s"
	}
	return "H/s"
}

func (p *discoverProvider) minerCalcCoins(ctx context.Context, btcUSD float64) ([]calcCoin, error) {
	return p.calcCoins.get(ctx, func(ctx context.Context) ([]calcCoin, error) {
		coins := make([]calcCoin, 0, 256)
		seen := make(map[string]bool)
		var firstErr error
		for _, endpoint := range []string{whatToMineCoins, whatToMineCPU, whatToMineASIC} {
			body, err := p.fetchText(ctx, endpoint, "", 25*time.Second, 32<<20)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			parsed, err := parseWhatToMineCoins(body, btcUSD)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, coin := range parsed {
				key := strings.ToUpper(coin.Tag) + "|" + coin.Algorithm
				if seen[key] {
					continue
				}
				seen[key] = true
				coins = append(coins, coin)
			}
		}
		if rows, err := p.miningPoolStatsRows(ctx); err == nil {
			annotateCalcCoinPages(coins, rows)
			coins = append(coins, newMiningPoolStatsCalcCoins(rows, seen)...)
		}
		if len(coins) == 0 {
			if firstErr != nil {
				return nil, fmt.Errorf("load mining calculator data: %w", firstErr)
			}
			return nil, fmt.Errorf("load mining calculator data: no coins available")
		}
		sort.SliceStable(coins, func(i, j int) bool { return coins[i].Name < coins[j].Name })
		return coins, nil
	})
}

func parseWhatToMineCoins(body string, btcUSD float64) ([]calcCoin, error) {
	var payload struct {
		Coins map[string]whatToMineCoin `json:"coins"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, err
	}
	coins := make([]calcCoin, 0, len(payload.Coins))
	for name, entry := range payload.Coins {
		if entry.Lagging {
			continue
		}
		// WhatToMine's "Nicehash-*" rows describe hashpower rental prices rather
		// than a mineable network, so their reward maths does not translate into a
		// coins-per-day estimate.
		if strings.EqualFold(strings.TrimSpace(entry.Tag), "NICEHASH") || strings.HasPrefix(strings.ToLower(name), "nicehash-") {
			continue
		}
		blockReward := entry.BlockReward24.float()
		if blockReward <= 0 {
			blockReward = entry.BlockReward.float()
		}
		coin := calcCoin{
			Name:            name,
			Tag:             strings.ToUpper(strings.TrimSpace(entry.Tag)),
			Algorithm:       strings.TrimSpace(entry.Algorithm),
			BlockTime:       entry.BlockTime.float(),
			BlockReward:     blockReward,
			NetworkHashrate: entry.NetHash.float(),
			Difficulty:      entry.Difficulty.float(),
			PriceUSD:        entry.ExchangeRate.float() * btcUSD,
			Source:          "whattomine",
		}
		coin.Unit = calcUnitForAlgorithm(coin.Algorithm)
		coin.CoinsPerDay = networkCoinsPerDay(coin.BlockTime, coin.BlockReward)
		if coin.NetworkHashrate <= 0 || coin.CoinsPerDay <= 0 {
			continue
		}
		coins = append(coins, coin)
	}
	return coins, nil
}

func newMiningPoolStatsCalcCoins(rows []mpsRow, seen map[string]bool) []calcCoin {
	coins := make([]calcCoin, 0)
	for _, row := range rows {
		tag := strings.ToUpper(strings.TrimSpace(row.Symbol))
		algorithm := strings.TrimSpace(row.Algo)
		if tag == "" || algorithm == "" {
			continue
		}
		if seen[tag+"|"+algorithm] {
			continue
		}
		blockTime := row.BlockTime.float()
		if blockTime <= 0 {
			blockTime = row.Target.float()
		}
		coin := calcCoin{
			Name:            strings.TrimSpace(row.Name),
			Tag:             tag,
			Algorithm:       algorithm,
			Unit:            strings.TrimSpace(row.Unit),
			BlockTime:       blockTime,
			NetworkHashrate: row.Hashrate.float(),
			Difficulty:      row.Difficulty.float(),
			PriceUSD:        row.Price.float(),
			CoinsPerDay:     row.Emissions24.float(),
			Source:          "miningpoolstats",
			RecentlyIndexed: row.Age.float() > 0,
			PageURL:         miningPoolStatsHome + strings.TrimLeft(strings.TrimSpace(row.Page), "/"),
		}

		if coin.Unit == "" {
			coin.Unit = calcUnitForAlgorithm(algorithm)
		}
		// MiningPoolStats occasionally publishes a near-zero network hashrate for
		// stale multi-algorithm pages, which would imply an impossible share of the
		// network and produce absurd earnings estimates.
		if coin.NetworkHashrate < minCalcNetworkHashrate || coin.CoinsPerDay <= 0 || coin.PriceUSD <= 0 {
			continue
		}
		coins = append(coins, coin)
	}
	return coins
}

func annotateCalcCoinPages(coins []calcCoin, rows []mpsRow) {
	byKey := make(map[string]string, len(rows))
	bySymbol := make(map[string]string, len(rows))
	for _, row := range rows {
		page := strings.TrimLeft(strings.TrimSpace(row.Page), "/")
		symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if page == "" || symbol == "" {
			continue
		}
		url := miningPoolStatsHome + page
		byKey[symbol+"|"+normalizeHardwareAlgorithm(row.Algo)] = url
		if _, exists := bySymbol[symbol]; !exists {
			bySymbol[symbol] = url
		}
	}
	for index := range coins {
		symbol := strings.ToUpper(strings.TrimSpace(coins[index].Tag))
		if page := byKey[symbol+"|"+normalizeHardwareAlgorithm(coins[index].Algorithm)]; page != "" {
			coins[index].PageURL = page
		} else if page := bySymbol[symbol]; page != "" {
			coins[index].PageURL = page
		}
	}
}

func networkCoinsPerDay(blockTimeSeconds, blockReward float64) float64 {
	if blockTimeSeconds <= 0 || blockReward <= 0 {
		return 0
	}
	return 86400 / blockTimeSeconds * blockReward
}
