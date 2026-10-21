package server

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	nonKYCMarketURL = "https://api.nonkyc.io/api/v2/market/getbysymbol/"
	nonKYCFeeRate   = 0.002
)

type nonKYCPriceCache struct {
	priceUSD float64
	pair     string
	expires  time.Time
}

type nonKYCMarket struct {
	Symbol            string    `json:"symbol"`
	BestBid           flexFloat `json:"bestBid"`
	SecondaryUSDValue flexFloat `json:"secondaryUsdValue"`
	IsActive          bool      `json:"isActive"`
	IsPaused          bool      `json:"isPaused"`
}

func (p *discoverProvider) applyNonKYCPrices(ctx context.Context, results []calcProfitResult, btcUSD float64) {
	var waitGroup sync.WaitGroup
	workers := make(chan struct{}, 6)
	for index := range results {
		if results[index].Source != "miningpoolstats" || results[index].EstimatedRewards <= 0 {
			continue
		}
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			select {
			case workers <- struct{}{}:
				defer func() { <-workers }()
			case <-ctx.Done():
				return
			}
			priceUSD, pair := p.nonKYCBestBidUSD(ctx, results[index].Tag)
			if priceUSD <= 0 {
				return
			}
			applyNonKYCPrice(&results[index], priceUSD, pair, btcUSD)
		}(index)
	}
	waitGroup.Wait()
}

func (p *discoverProvider) nonKYCBestBidUSD(ctx context.Context, symbol string) (float64, string) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return 0, ""
	}
	now := time.Now()
	p.nonKYCMu.Lock()
	cached, found := p.nonKYCPrices[symbol]
	p.nonKYCMu.Unlock()
	if found && now.Before(cached.expires) {
		return cached.priceUSD, cached.pair
	}

	priceUSD, pair := 0.0, ""
	for _, quote := range []string{"USDT", "USDC", "BTC"} {
		marketSymbol := symbol + "_" + quote
		body, err := p.fetchText(ctx, nonKYCMarketURL+url.PathEscape(marketSymbol), "https://nonkyc.io/", 8*time.Second, 1<<20)
		if err != nil {
			continue
		}
		var market nonKYCMarket
		if json.Unmarshal([]byte(body), &market) != nil || !market.IsActive || market.IsPaused {
			continue
		}
		bid := market.BestBid.float()
		quoteUSD := market.SecondaryUSDValue.float()
		if bid <= 0 || quoteUSD <= 0 {
			continue
		}
		priceUSD = bid * quoteUSD * (1 - nonKYCFeeRate)
		pair = strings.TrimSpace(market.Symbol)
		break
	}

	p.nonKYCMu.Lock()
	p.nonKYCPrices[symbol] = nonKYCPriceCache{priceUSD: priceUSD, pair: pair, expires: now.Add(10 * time.Minute)}
	p.nonKYCMu.Unlock()
	return priceUSD, pair
}

func applyNonKYCPrice(result *calcProfitResult, priceUSD float64, pair string, btcUSD float64) {
	result.PriceUSD = priceUSD
	result.RevenueUSD = result.EstimatedRewards * priceUSD
	if btcUSD > 0 {
		result.BTCRevenue = result.RevenueUSD / btcUSD
	}
	result.Exchange = "NonKYC"
	result.Status = "Active · NonKYC bid after 0.2% fee"
	if pair != "" {
		result.Status += " · " + pair
	}
}
