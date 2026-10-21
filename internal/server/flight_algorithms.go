package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const minerAlgorithmCatalogURL = "https://raw.githubusercontent.com/OBitsPlease/Miner-Dash/main/internal/server/miner_algorithms.json"

//go:embed miner_algorithms.json
var bundledMinerAlgorithms []byte

var minerAlgorithmNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./+-]{0,63}$`)

type minerAlgorithmCatalog struct {
	SchemaVersion int                   `json:"schema_version"`
	UpdatedAt     string                `json:"updated_at"`
	Miners        []minerAlgorithmEntry `json:"miners"`
	Source        string                `json:"-"`
}

type minerAlgorithmEntry struct {
	ID         string            `json:"id"`
	Algorithms []string          `json:"algorithms"`
	Aliases    map[string]string `json:"aliases"`
}

type flightAlgorithmResponse struct {
	ResolvedAlgorithm    string   `json:"resolved_algorithm"`
	ResolutionSource     string   `json:"resolution_source,omitempty"`
	CoinAlgorithms       []string `json:"coin_algorithms"`
	CompatibleAlgorithms []string `json:"compatible_algorithms"`
	MinerAlgorithms      []string `json:"miner_algorithms"`
	CatalogUpdatedAt     string   `json:"catalog_updated_at,omitempty"`
	CatalogSource        string   `json:"catalog_source"`
	ResolutionError      string   `json:"resolution_error,omitempty"`
}

func (p *discoverProvider) minerAlgorithmCatalog(ctx context.Context) (minerAlgorithmCatalog, string, error) {
	catalog, err := p.minerAlgos.get(ctx, func(ctx context.Context) (minerAlgorithmCatalog, error) {
		body, fetchErr := p.fetchText(ctx, minerAlgorithmCatalogURL, "", 10*time.Second, 2<<20)
		if fetchErr == nil {
			if remote, parseErr := parseMinerAlgorithmCatalog([]byte(body)); parseErr == nil {
				remote.Source = "remote"
				return remote, nil
			}
		}
		bundled, parseErr := parseMinerAlgorithmCatalog(bundledMinerAlgorithms)
		bundled.Source = "bundled"
		return bundled, parseErr
	})
	if err != nil {
		return minerAlgorithmCatalog{}, "", err
	}
	return catalog, catalog.Source, nil
}

func parseMinerAlgorithmCatalog(body []byte) (minerAlgorithmCatalog, error) {
	var catalog minerAlgorithmCatalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return minerAlgorithmCatalog{}, fmt.Errorf("decode miner algorithm catalog: %w", err)
	}
	if catalog.SchemaVersion != 1 {
		return minerAlgorithmCatalog{}, fmt.Errorf("unsupported miner algorithm catalog schema %d", catalog.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, catalog.UpdatedAt); err != nil {
		return minerAlgorithmCatalog{}, errors.New("miner algorithm catalog has an invalid updated_at")
	}
	known := make(map[string]bool, len(minerCatalog))
	for _, miner := range minerCatalog {
		known[miner.ID] = true
	}
	seen := make(map[string]bool, len(catalog.Miners))
	for index := range catalog.Miners {
		entry := &catalog.Miners[index]
		entry.ID = strings.TrimSpace(entry.ID)
		if !known[entry.ID] || seen[entry.ID] {
			return minerAlgorithmCatalog{}, fmt.Errorf("miner algorithm catalog has invalid or duplicate miner %q", entry.ID)
		}
		seen[entry.ID] = true
		algorithmSet := make(map[string]bool, len(entry.Algorithms))
		cleaned := make([]string, 0, len(entry.Algorithms))
		for _, algorithm := range entry.Algorithms {
			algorithm = strings.TrimSpace(algorithm)
			key := normalizeHardwareAlgorithm(algorithm)
			if !minerAlgorithmNamePattern.MatchString(algorithm) || algorithmSet[key] {
				return minerAlgorithmCatalog{}, fmt.Errorf("miner %q has invalid or duplicate algorithm %q", entry.ID, algorithm)
			}
			algorithmSet[key] = true
			cleaned = append(cleaned, algorithm)
		}
		if len(cleaned) == 0 {
			return minerAlgorithmCatalog{}, fmt.Errorf("miner %q has no algorithms", entry.ID)
		}
		sort.SliceStable(cleaned, func(i, j int) bool { return strings.ToLower(cleaned[i]) < strings.ToLower(cleaned[j]) })
		entry.Algorithms = cleaned
		aliases := make(map[string]string, len(entry.Aliases))
		for alias, target := range entry.Aliases {
			if normalizeHardwareAlgorithm(alias) == "" || !algorithmSet[normalizeHardwareAlgorithm(target)] {
				return minerAlgorithmCatalog{}, fmt.Errorf("miner %q has invalid alias %q", entry.ID, alias)
			}
			aliases[normalizeHardwareAlgorithm(alias)] = target
		}
		entry.Aliases = aliases
	}
	if len(seen) != len(known) {
		return minerAlgorithmCatalog{}, errors.New("miner algorithm catalog does not cover every built-in miner")
	}
	return catalog, nil
}

func (catalog minerAlgorithmCatalog) entry(id string) (minerAlgorithmEntry, bool) {
	for _, entry := range catalog.Miners {
		if entry.ID == id {
			return entry, true
		}
	}
	return minerAlgorithmEntry{}, false
}

func validateBundledMinerAlgorithm(minerID, algorithm string) error {
	algorithm = strings.TrimSpace(algorithm)
	if algorithm == "" || minerID != "wildrig-multi" {
		return nil
	}
	catalog, err := parseMinerAlgorithmCatalog(bundledMinerAlgorithms)
	if err != nil {
		return fmt.Errorf("validate miner algorithm: %w", err)
	}
	entry, ok := catalog.entry(minerID)
	if !ok {
		return nil
	}
	if len(compatibleMinerAlgorithms([]string{algorithm}, entry.Algorithms, entry.Aliases)) > 0 {
		return nil
	}
	if minerID == "wildrig-multi" && strings.EqualFold(algorithm, "progpow") {
		return errors.New("WildRig Multi does not support generic progpow in the installed catalog; the Veil pool's WildRig v0.40.6 recommendation is AMD-specific. This NVIDIA rig needs a miner release that explicitly supports NVIDIA Veil ProgPow")
	}
	return fmt.Errorf("%s does not support algorithm %q", minerID, algorithm)
}

func catalogAlgorithmForCoin(minerID, coin, algorithm string) string {
	if minerID == "trex" &&
		strings.EqualFold(strings.TrimSpace(coin), "VEIL") &&
		normalizeHardwareAlgorithm(algorithm) == normalizeHardwareAlgorithm("progpow") {
		return "progpow-veil"
	}
	if minerID == "veil-miner-cpu" &&
		strings.EqualFold(strings.TrimSpace(coin), "VEIL") {
		return "rx/veil"
	}
	if minerID == "veil-miner-gpu" &&
		strings.EqualFold(strings.TrimSpace(coin), "VEIL") {
		return "progpow-veil"
	}
	return algorithm
}

func validateCatalogMinerCoin(minerID, coin string) error {
	if minerID == "xmrig" && strings.EqualFold(strings.TrimSpace(coin), "VEIL") {
		return errors.New("stock XMRig does not support Veil's rx/veil variant; select Veil CPU Miner instead")
	}
	if minerID == "veil-miner-cpu" && !strings.EqualFold(strings.TrimSpace(coin), "VEIL") {
		return errors.New("Veil CPU Miner supports only VEIL")
	}
	if minerID == "veil-miner-gpu" && !strings.EqualFold(strings.TrimSpace(coin), "VEIL") {
		return errors.New("Veil GPU Miner supports only VEIL")
	}
	return nil
}

func (p *discoverProvider) resolveFlightAlgorithms(ctx context.Context, coin, deviceType, minerKind, minerID string, installed []protocolMinerAlgorithm) flightAlgorithmResponse {
	response := flightAlgorithmResponse{
		CoinAlgorithms:       []string{},
		CompatibleAlgorithms: []string{},
		MinerAlgorithms:      []string{},
		CatalogSource:        "unavailable",
	}
	catalog, source, catalogErr := p.minerAlgorithmCatalog(ctx)
	if catalogErr == nil {
		response.CatalogUpdatedAt = catalog.UpdatedAt
		response.CatalogSource = source
		if minerKind == "catalog" {
			if entry, ok := catalog.entry(minerID); ok {
				response.MinerAlgorithms = append(response.MinerAlgorithms, entry.Algorithms...)
			}
		}
	}
	if minerKind == "community" || minerKind == "miner" {
		for _, miner := range installed {
			if miner.Kind == minerKind && miner.ID == minerID && strings.TrimSpace(miner.Algorithm) != "" {
				response.MinerAlgorithms = appendUniqueAlgorithm(response.MinerAlgorithms, miner.Algorithm)
			}
		}
	}

	if strings.TrimSpace(coin) != "" {
		rows, marketErr := p.miningPoolStatsRows(ctx)
		response.CoinAlgorithms = matchingMPSAlgorithms(rows, coin, deviceType)
		if len(response.CoinAlgorithms) > 0 {
			response.ResolutionSource = "MiningPoolStats"
		} else {
			listings, listingErr := p.newListings(ctx)
			if listingErr == nil {
				response.CoinAlgorithms = matchingListingAlgorithms(listings, coin)
				if len(response.CoinAlgorithms) > 0 {
					response.ResolutionSource = "New Listings"
				}
			} else if marketErr != nil {
				response.ResolutionError = "coin algorithm sources are temporarily unavailable"
			}
		}
	}

	var entry minerAlgorithmEntry
	if minerKind == "catalog" {
		entry, _ = catalog.entry(minerID)
	}
	response.CompatibleAlgorithms = compatibleMinerAlgorithms(response.CoinAlgorithms, response.MinerAlgorithms, entry.Aliases)
	if len(response.CompatibleAlgorithms) > 0 {
		response.ResolvedAlgorithm = catalogAlgorithmForCoin(minerID, coin, response.CompatibleAlgorithms[0])
	}
	return response
}

type protocolMinerAlgorithm struct {
	Kind      string
	ID        string
	Algorithm string
}

func matchingMPSAlgorithms(rows []mpsRow, coin, deviceType string) []string {
	key := normalizeCoinLookup(coin)
	if key == "" {
		return []string{}
	}
	symbolMatches := make([]mpsRow, 0)
	nameMatches := make([]mpsRow, 0)
	for _, row := range rows {
		if normalizeCoinLookup(row.Symbol) == key {
			symbolMatches = append(symbolMatches, row)
		} else if normalizeCoinLookup(row.Name) == key {
			nameMatches = append(nameMatches, row)
		}
	}
	matches := symbolMatches
	if len(matches) == 0 {
		matches = nameMatches
	}
	filtered := matches[:0]
	for _, row := range matches {
		if deviceType == "" || strings.EqualFold(strings.TrimSpace(row.Type), strings.TrimSpace(deviceType)) {
			filtered = append(filtered, row)
		}
	}
	if len(filtered) > 0 {
		matches = filtered
	}
	algorithms := make([]string, 0, len(matches))
	for _, row := range matches {
		algorithms = appendUniqueAlgorithm(algorithms, row.Algo)
	}
	return algorithms
}

func matchingListingAlgorithms(listings []newListing, coin string) []string {
	key := normalizeCoinLookup(coin)
	if key == "" {
		return []string{}
	}
	algorithms := make([]string, 0, 1)
	for _, listing := range listings {
		if normalizeCoinLookup(listing.Symbol) == key || normalizeCoinLookup(listing.Name) == key {
			algorithms = appendUniqueAlgorithm(algorithms, listing.Algo)
		}
	}
	return algorithms
}

func selectMinerAlgorithm(candidates, supported []string, aliases map[string]string) string {
	compatible := compatibleMinerAlgorithms(candidates, supported, aliases)
	if len(compatible) == 0 {
		return ""
	}
	return compatible[0]
}

func compatibleMinerAlgorithms(candidates, supported []string, aliases map[string]string) []string {
	if len(candidates) == 0 {
		return []string{}
	}
	if len(supported) == 0 {
		result := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			result = appendUniqueAlgorithm(result, candidate)
		}
		return result
	}
	byKey := make(map[string]string, len(supported))
	for _, algorithm := range supported {
		byKey[normalizeHardwareAlgorithm(algorithm)] = algorithm
	}
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		key := normalizeHardwareAlgorithm(candidate)
		if target := aliases[key]; target != "" {
			if resolved := byKey[normalizeHardwareAlgorithm(target)]; resolved != "" {
				result = appendUniqueAlgorithm(result, resolved)
				continue
			}
		}
		if resolved := byKey[key]; resolved != "" {
			result = appendUniqueAlgorithm(result, resolved)
		}
	}
	return result
}

func appendUniqueAlgorithm(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return values
	}
	key := normalizeHardwareAlgorithm(candidate)
	for _, value := range values {
		if normalizeHardwareAlgorithm(value) == key {
			return values
		}
	}
	return append(values, candidate)
}

func normalizeCoinLookup(value string) string {
	return normalizeHardwareAlgorithm(value)
}
