package server

import (
	"strings"
	"testing"
)

func TestBundledMinerAlgorithmCatalogCoversBuiltInMiners(t *testing.T) {
	catalog, err := parseMinerAlgorithmCatalog(bundledMinerAlgorithms)
	if err != nil {
		t.Fatalf("parse bundled catalog: %v", err)
	}
	if len(catalog.Miners) != len(minerCatalog) {
		t.Fatalf("catalog has %d miners, expected %d", len(catalog.Miners), len(minerCatalog))
	}
	xmrig, _ := catalog.entry("xmrig")
	if resolved := selectMinerAlgorithm([]string{"RandomX"}, xmrig.Algorithms, xmrig.Aliases); resolved != "rx/0" {
		t.Fatalf("RandomX resolved to %q for XMRig, expected rx/0", resolved)
	}
	for minerID, algorithms := range map[string][]string{
		"rigel":          {"abelian", "karlsenhashv2", "xelishashv3"},
		"wildrig-multi":  {"anime", "curvehash", "meowpow", "x33"},
		"srbminer-multi": {"flex", "xelishashv3", "randomx", "rx/veil", "yespowerurx"},
		"lolminer":       {"karlsenhashv2", "pyrinv2", "sha3x"},
		"teamredminer":   {"ironfish", "kaspa", "pyrin", "ton"},
		"bzminer":        {"c29", "quantus", "warthog", "xelis"},
		"gminer":         {"kheavyhash", "cuckatoo31"},
		"trex":           {"mtp-tcr", "progpow-veil", "tensority"},
		"onezerominer":   {"dynex", "xelishashv3", "quantus"},
		"cpuminer-opt":   {"x11evo", "yespower-b2b", "sha3d"},
	} {
		entry, ok := catalog.entry(minerID)
		if !ok {
			t.Fatalf("required catalog miner %q is missing", minerID)
		}
		for _, algorithm := range algorithms {
			if !containsNormalized(entry.Algorithms, algorithm) {
				t.Errorf("miner %s is missing documented algorithm %s", minerID, algorithm)
			}
		}
	}
}

func containsNormalized(values []string, target string) bool {
	target = normalizeHardwareAlgorithm(target)
	for _, value := range values {
		if normalizeHardwareAlgorithm(value) == target {
			return true
		}
	}
	return false
}

func TestMatchingMPSAlgorithmsPrefersSymbolAndWorkloadType(t *testing.T) {
	rows := []mpsRow{
		{Name: "Veil", Symbol: "VEIL", Algo: "ProgPow", Type: "GPU"},
		{Name: "Not Veil", Symbol: "OTHER", Algo: "KawPow", Type: "GPU"},
	}
	algorithms := matchingMPSAlgorithms(rows, "veil", "GPU")
	if len(algorithms) != 1 || algorithms[0] != "ProgPow" {
		t.Fatalf("GPU VEIL algorithms = %#v", algorithms)
	}
}

func TestNewListingAlgorithmFallbackMatchesCoinNameOrSymbol(t *testing.T) {
	listings := []newListing{
		{Name: "Brand New Coin", Symbol: "BNC", Algo: "Yespower"},
		{Name: "Other Coin", Symbol: "OTHER", Algo: "KawPow"},
	}
	for _, coin := range []string{"BNC", "brand new coin"} {
		algorithms := matchingListingAlgorithms(listings, coin)
		if len(algorithms) != 1 || algorithms[0] != "Yespower" {
			t.Fatalf("%q algorithms = %#v", coin, algorithms)
		}
	}
}

func TestCoinAlgorithmDoesNotAutofillUnsupportedMinerValue(t *testing.T) {
	if resolved := selectMinerAlgorithm([]string{"KawPow"}, []string{"rx/0", "ghostrider"}, map[string]string{"randomx": "rx/0"}); resolved != "" {
		t.Fatalf("unsupported KawPow resolved to %q", resolved)
	}
}

func TestWildRigRejectsGenericProgPowForVeil(t *testing.T) {
	err := validateBundledMinerAlgorithm("wildrig-multi", "progpow")
	if err == nil || !strings.Contains(err.Error(), "Veil") || !strings.Contains(err.Error(), "NVIDIA") {
		t.Fatalf("expected actionable WildRig Veil compatibility error, got %v", err)
	}
}

func TestTReXUsesVeilSpecificProgPowVariant(t *testing.T) {
	if got := catalogAlgorithmForCoin("trex", "VEIL", "ProgPow"); got != "progpow-veil" {
		t.Fatalf("T-Rex VEIL algorithm = %q", got)
	}
	if got := catalogAlgorithmForCoin("trex", "OTHER", "progpow"); got != "progpow" {
		t.Fatalf("T-Rex non-VEIL algorithm = %q", got)
	}
}

func TestVeilCPUMinerUsesVeilSpecificRandomXVariant(t *testing.T) {
	for _, algorithm := range []string{"RandomX", "rx/0"} {
		if got := catalogAlgorithmForCoin("veil-miner-cpu", "VEIL", algorithm); got != "rx/veil" {
			t.Fatalf("Veil CPU algorithm %q resolved to %q", algorithm, got)
		}
	}
	if got := catalogAlgorithmForCoin("xmrig", "XMR", "RandomX"); got != "RandomX" {
		t.Fatalf("XMRig non-VEIL algorithm = %q", got)
	}
}

func TestVeilGPUCatalogAndAlgorithm(t *testing.T) {
	entry, ok := catalogEntry("veil-miner-gpu")
	if !ok || entry.SourceURL != "https://github.com/Veil-Project/Veil-Miner/releases" {
		t.Fatalf("Veil GPU catalog entry = %#v, found=%v", entry, ok)
	}
	if got := catalogAlgorithmForCoin("veil-miner-gpu", "VEIL", "ProgPow"); got != "progpow-veil" {
		t.Fatalf("Veil GPU algorithm = %q", got)
	}
	if err := validateCatalogMinerCoin("veil-miner-gpu", "VEIL"); err != nil {
		t.Fatal(err)
	}
	if err := validateCatalogMinerCoin("veil-miner-gpu", "XMR"); err == nil {
		t.Fatal("Veil GPU miner accepted a non-VEIL coin")
	}
}
func TestStockXMRigRejectsVeil(t *testing.T) {
	err := validateCatalogMinerCoin("xmrig", "VEIL")
	if err == nil || !strings.Contains(err.Error(), "Veil CPU Miner") {
		t.Fatalf("stock XMRig VEIL error = %v", err)
	}
	if err := validateCatalogMinerCoin("xmrig", "XMR"); err != nil {
		t.Fatalf("stock XMRig rejected XMR: %v", err)
	}
}
