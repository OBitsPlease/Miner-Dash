package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type calcProfitInput struct {
	Algorithm string  `json:"algorithm"`
	Hashrate  float64 `json:"hashrate"`
	Watts     float64 `json:"watts"`
}

type calcProfitResult struct {
	Name             string  `json:"name"`
	Tag              string  `json:"tag"`
	Algorithm        string  `json:"algorithm"`
	EstimatedRewards float64 `json:"estimated_rewards"`
	BTCRevenue       float64 `json:"btc_revenue"`
	RevenueUSD       float64 `json:"revenue_usd"`
	BlockReward      float64 `json:"block_reward"`
	Status           string  `json:"status"`
	Listed           bool    `json:"listed"`
	IsNew            bool    `json:"is_new"`
	PageURL          string  `json:"page_url"`
	Source           string  `json:"source"`
	PriceUSD         float64 `json:"price_usd"`
	Exchange         string  `json:"exchange"`
}

type wtmProfitConfig struct {
	Endpoint   string
	Key        string
	Toggle     string
	HashField  string
	PowerField string
	Divisor    float64
}

var wtmProfitConfigs = []wtmProfitConfig{
	{Endpoint: whatToMineCoins, Key: "kawpow", Toggle: "kpw", HashField: "kpw_hr", PowerField: "kpw_p", Divisor: 1e6},
	{Endpoint: whatToMineCoins, Key: "ethash", Toggle: "eth", HashField: "eth_hr", PowerField: "eth_p", Divisor: 1e6},
	{Endpoint: whatToMineCoins, Key: "etchash", Toggle: "e4g", HashField: "e4g_hr", PowerField: "e4g_p", Divisor: 1e6},
	{Endpoint: whatToMineCoins, Key: "autolykos", Toggle: "al", HashField: "al_hr", PowerField: "al_p", Divisor: 1e6},
	{Endpoint: whatToMineCoins, Key: "firopow", Toggle: "fpw", HashField: "fpw_hr", PowerField: "fpw_p", Divisor: 1e6},
	{Endpoint: whatToMineCoins, Key: "equihash", Toggle: "zh", HashField: "zh_hr", PowerField: "zh_p", Divisor: 1},
	{Endpoint: whatToMineCoins, Key: "blake3", Toggle: "b3", HashField: "b3_hr", PowerField: "b3_p", Divisor: 1e9},
	{Endpoint: whatToMineCPU, Key: "randomx", Toggle: "rmx", HashField: "rmx_hr", PowerField: "rmx_p", Divisor: 1e3},
	{Endpoint: whatToMineCPU, Key: "ghostrider", Toggle: "gr", HashField: "gr_hr", PowerField: "gr_p", Divisor: 1e3},
	{Endpoint: whatToMineASIC, Key: "sha256", Toggle: "sha256f", HashField: "sha256_hr", PowerField: "sha256_p", Divisor: 1e12},
	{Endpoint: whatToMineASIC, Key: "scrypt", Toggle: "scryptf", HashField: "scrypt_hash_rate", PowerField: "scrypt_power", Divisor: 1e9},
}

type wtmProfitCoin struct {
	Tag                string    `json:"tag"`
	Algorithm          string    `json:"algorithm"`
	EstimatedRewards   flexFloat `json:"estimated_rewards"`
	EstimatedRewards24 flexFloat `json:"estimated_rewards24"`
	BTCRevenue         flexFloat `json:"btc_revenue"`
	BTCRevenue24       flexFloat `json:"btc_revenue24"`
	BlockReward        flexFloat `json:"block_reward"`
	Status             string    `json:"status"`
	Listed             bool      `json:"listed"`
	Lagging            bool      `json:"lagging"`
}

func normalizedCalcAlgorithm(algorithm string) string {
	key := normalizeHardwareAlgorithm(algorithm)
	switch key {
	case "autolykos2", "autolykosv2":
		return "autolykos"
	case "zhash", "equihash1445", "equihash1927", "equihash1254":
		return "equihash"
	default:
		return key
	}
}

func profitConfigForAlgorithm(algorithm string) (wtmProfitConfig, bool) {
	key := normalizedCalcAlgorithm(algorithm)
	for _, config := range wtmProfitConfigs {
		if config.Key == key {
			return config, true
		}
	}
	return wtmProfitConfig{}, false
}

func buildWhatToMineProfitURLs(inputs []calcProfitInput) map[string]string {
	queries := make(map[string]url.Values)
	for _, input := range inputs {
		config, ok := profitConfigForAlgorithm(input.Algorithm)
		if !ok || input.Hashrate <= 0 {
			continue
		}
		values := queries[config.Endpoint]
		if values == nil {
			values = make(url.Values)
			queries[config.Endpoint] = values
		}
		values.Set(config.Toggle, "true")
		values.Set("factor["+config.HashField+"]", strconv.FormatFloat(input.Hashrate/config.Divisor, 'f', -1, 64))
		values.Set("factor["+config.PowerField+"]", strconv.FormatFloat(input.Watts, 'f', -1, 64))
	}
	result := make(map[string]string, len(queries))
	for endpoint, values := range queries {
		result[endpoint] = endpoint + "?" + values.Encode()
	}
	return result
}

func (p *discoverProvider) minerProfitability(ctx context.Context, inputs []calcProfitInput, btcUSD float64) ([]calcProfitResult, error) {
	urls := buildWhatToMineProfitURLs(inputs)
	results := make([]calcProfitResult, 0, 64)
	var firstErr error
	for endpoint, requestURL := range urls {
		body, err := p.fetchText(ctx, requestURL, endpoint, 20*time.Second, 32<<20)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		parsed, err := parseWhatToMineProfitability(body, inputs, btcUSD)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		results = append(results, parsed...)
	}

	coins, coinErr := p.minerCalcCoins(ctx, btcUSD)
	if coinErr == nil {
		results = append(results, customProfitabilityResults(coins, inputs)...)
	} else if firstErr == nil {
		firstErr = coinErr
	}
	if rows, err := p.miningPoolStatsRows(ctx); err == nil {
		annotateProfitabilityPages(results, rows)
	}
	p.applyNonKYCPrices(ctx, results, btcUSD)
	results = dedupeProfitabilityResults(results)
	if len(results) == 0 && firstErr != nil {
		return nil, fmt.Errorf("load profitability: %w", firstErr)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].RevenueUSD > results[j].RevenueUSD })
	return results, nil
}

func parseWhatToMineProfitability(body string, inputs []calcProfitInput, btcUSD float64) ([]calcProfitResult, error) {
	var payload struct {
		Coins map[string]wtmProfitCoin `json:"coins"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, err
	}
	enabled := make(map[string]bool)
	for _, input := range inputs {
		if input.Hashrate > 0 {
			enabled[normalizedCalcAlgorithm(input.Algorithm)] = true
		}
	}
	results := make([]calcProfitResult, 0, len(payload.Coins))
	for name, coin := range payload.Coins {
		key := normalizedCalcAlgorithm(coin.Algorithm)
		if strings.HasPrefix(strings.ToLower(name), "nicehash-") {
			for algorithm := range enabled {
				if strings.Contains(normalizedCalcAlgorithm(name), algorithm) {
					key = algorithm
					break
				}
			}
		}
		if coin.Lagging || !enabled[key] {
			continue
		}
		btcRevenue := coin.BTCRevenue24.float()
		if btcRevenue <= 0 {
			btcRevenue = coin.BTCRevenue.float()
		}
		if btcRevenue <= 0 {
			continue
		}
		rewards := coin.EstimatedRewards24.float()
		if rewards <= 0 {
			rewards = coin.EstimatedRewards.float()
		}
		priceUSD := 0.0
		if rewards > 0 {
			priceUSD = btcRevenue * btcUSD / rewards
		}
		results = append(results, calcProfitResult{
			Name:             name,
			Tag:              strings.ToUpper(strings.TrimSpace(coin.Tag)),
			Algorithm:        strings.TrimSpace(coin.Algorithm),
			EstimatedRewards: rewards,
			BTCRevenue:       btcRevenue,
			RevenueUSD:       btcRevenue * btcUSD,
			BlockReward:      coin.BlockReward.float(),
			Status:           strings.TrimSpace(coin.Status),
			Listed:           coin.Listed,
			Source:           "whattomine",
			PriceUSD:         priceUSD,
			Exchange:         "WhatToMine",
		})
	}
	return results, nil
}

func customProfitabilityResults(coins []calcCoin, inputs []calcProfitInput) []calcProfitResult {
	byAlgorithm := make(map[string]calcProfitInput, len(inputs))
	recognized := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		key := normalizedCalcAlgorithm(input.Algorithm)
		byAlgorithm[key] = input
		_, recognized[key] = profitConfigForAlgorithm(input.Algorithm)
	}
	results := make([]calcProfitResult, 0)
	for _, coin := range coins {
		key := normalizedCalcAlgorithm(coin.Algorithm)
		input, ok := byAlgorithm[key]
		if !ok || input.Hashrate <= 0 || coin.NetworkHashrate < minCalcNetworkHashrate {
			continue
		}
		if recognized[key] && !(coin.Source == "miningpoolstats" && coin.RecentlyIndexed) {
			continue
		}
		share := input.Hashrate / coin.NetworkHashrate
		rewards := share * coin.CoinsPerDay
		results = append(results, calcProfitResult{
			Name:             coin.Name,
			Tag:              coin.Tag,
			Algorithm:        coin.Algorithm,
			EstimatedRewards: rewards,
			RevenueUSD:       rewards * coin.PriceUSD,
			PriceUSD:         coin.PriceUSD,
			BlockReward:      coin.BlockReward,
			Status:           "Active · MiningPoolStats estimate",
			IsNew:            coin.IsNew,
			PageURL:          coin.PageURL,
			Source:           coin.Source,
			Exchange:         "MiningPoolStats",
		})
	}
	return results
}

func annotateProfitabilityPages(results []calcProfitResult, rows []mpsRow) {
	coins := make([]calcCoin, len(results))
	for index, result := range results {
		coins[index] = calcCoin{Tag: result.Tag, Algorithm: result.Algorithm}
	}
	annotateCalcCoinPages(coins, rows)
	for index := range results {
		if results[index].PageURL == "" {
			results[index].PageURL = coins[index].PageURL
		}
	}
}

func dedupeProfitabilityResults(results []calcProfitResult) []calcProfitResult {
	seen := make(map[string]bool, len(results))
	deduped := results[:0]
	for _, result := range results {
		key := strings.ToLower(strings.TrimSpace(result.Name)) + "|" + normalizedCalcAlgorithm(result.Algorithm)
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, result)
	}
	return deduped
}
