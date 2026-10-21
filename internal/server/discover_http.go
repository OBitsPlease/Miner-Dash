package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *HTTPServer) discoverPOWPrices(w http.ResponseWriter, r *http.Request) {
	coins, err := s.discover.powPrices(r.Context())
	if err != nil {
		s.logger.Printf("discover pow prices: %v", err)
		http.Error(w, "proof-of-work prices are temporarily unavailable", http.StatusBadGateway)
		return
	}
	if geckoErr := s.discover.coinGeckoStatus(); geckoErr != nil {
		s.logger.Printf("discover pow prices: short-term price changes unavailable: %v", geckoErr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"coins": coins, "updated_at": time.Now().UTC()})
}

func (s *HTTPServer) discoverNewListings(w http.ResponseWriter, r *http.Request) {
	listings, err := s.discover.newListings(r.Context())
	if err != nil {
		s.logger.Printf("discover new listings: %v", err)
		http.Error(w, "new listings are temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": listings, "updated_at": time.Now().UTC()})
}

func (s *HTTPServer) discoverEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.discover.calendarEvents(r.Context())
	if err != nil {
		s.logger.Printf("discover events: %v", err)
		http.Error(w, "upcoming events are temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *HTTPServer) discoverCalcAlgorithms(w http.ResponseWriter, _ *http.Request) {
	cpu := make(map[string]bool)
	gpu := make(map[string]bool)
	for _, miner := range minerCatalog {
		entry, ok := catalogEntry(miner.ID)
		if !ok {
			continue
		}
		algorithms, err := parseMinerAlgorithmCatalog(bundledMinerAlgorithms)
		if err != nil {
			http.Error(w, "calculator algorithm catalog is unavailable", http.StatusInternalServerError)
			return
		}
		capability, ok := algorithms.entry(entry.ID)
		if !ok {
			continue
		}
		for _, algorithm := range capability.Algorithms {
			key := normalizeHardwareAlgorithm(algorithm)
			for _, hardware := range entry.Hardware {
				if hardware == "CPU" {
					cpu[key] = true
				} else {
					gpu[key] = true
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cpu": sortedAlgorithmKeys(cpu), "gpu": sortedAlgorithmKeys(gpu),
		"asic": []string{"sha256d", "scrypt", "x11", "equihash"},
	})
}

func sortedAlgorithmKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func (s *HTTPServer) discoverCalcCoins(w http.ResponseWriter, r *http.Request) {
	btcUSD := 0.0
	if prices, err := s.market.prices(r.Context()); err == nil {
		btcUSD = prices["BTC"].USD
	}
	coins, err := s.discover.minerCalcCoins(r.Context(), btcUSD)
	if err != nil {
		s.logger.Printf("discover miner calc: %v", err)
		http.Error(w, "mining calculator data is temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coins": coins, "btc_usd": btcUSD, "updated_at": time.Now().UTC()})
}

func (s *HTTPServer) discoverCalcProfitability(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Algorithms []calcProfitInput `json:"algorithms"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "send a valid algorithms array", http.StatusBadRequest)
		return
	}
	if len(request.Algorithms) == 0 || len(request.Algorithms) > 100 {
		http.Error(w, "send between 1 and 100 algorithms", http.StatusBadRequest)
		return
	}
	for _, input := range request.Algorithms {
		if strings.TrimSpace(input.Algorithm) == "" || input.Hashrate <= 0 || input.Watts < 0 {
			http.Error(w, "each algorithm requires a name, a positive hashrate, and non-negative watts", http.StatusBadRequest)
			return
		}
	}
	btcUSD := 0.0
	if prices, err := s.market.prices(r.Context()); err == nil {
		btcUSD = prices["BTC"].USD
	}
	results, err := s.discover.minerProfitability(r.Context(), request.Algorithms, btcUSD)
	if err != nil {
		s.logger.Printf("discover miner profitability: %v", err)
		http.Error(w, "live profitability is temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coins": results, "btc_usd": btcUSD, "updated_at": time.Now().UTC()})
}

func (s *HTTPServer) discoverCalcHardware(w http.ResponseWriter, r *http.Request) {
	hardware, err := s.discover.calcHardware(r.Context())
	if err != nil {
		s.logger.Printf("discover calc hardware: %v", err)
		http.Error(w, "mining hardware data is temporarily unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hardware": hardware, "updated_at": time.Now().UTC()})
}

func (s *HTTPServer) discoverCalcBenchmarks(w http.ResponseWriter, r *http.Request) {
	benchmarks, err := s.discover.calcHardwareBenchmarks(r.Context(), r.URL.Query().Get("type"), r.URL.Query().Get("id"))
	if err != nil {
		s.logger.Printf("discover calc benchmarks: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"benchmarks": benchmarks})
}

func (s *HTTPServer) flightSheetAlgorithms(w http.ResponseWriter, r *http.Request) {
	coin := strings.TrimSpace(r.URL.Query().Get("coin"))
	deviceType := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("device_type")))
	minerKind := strings.TrimSpace(r.URL.Query().Get("miner_kind"))
	minerID := strings.TrimSpace(r.URL.Query().Get("miner_id"))
	if len(coin) > 64 || len(minerKind) > 32 || len(minerID) > 128 {
		http.Error(w, "flight-sheet algorithm query is too long", http.StatusBadRequest)
		return
	}
	if deviceType != "" && deviceType != "CPU" && deviceType != "GPU" && deviceType != "ASIC" {
		http.Error(w, "device_type must be CPU, GPU, or ASIC", http.StatusBadRequest)
		return
	}
	installed := make([]protocolMinerAlgorithm, 0)
	for _, miner := range s.store.ListMiners() {
		installed = append(installed, protocolMinerAlgorithm{Kind: "miner", ID: miner.ID, Algorithm: miner.Algorithm})
		if miner.CatalogID != "" {
			installed = append(installed, protocolMinerAlgorithm{Kind: "catalog", ID: miner.CatalogID, Algorithm: miner.Algorithm})
		}
	}
	if minerKind == "community" && minerID != "" {
		catalog := s.community.catalog(r.Context())
		for _, miner := range catalog.Miners {
			if miner.ID == minerID {
				installed = append(installed, protocolMinerAlgorithm{Kind: "community", ID: miner.ID, Algorithm: miner.RecommendedAlgorithm})
				break
			}
		}
	}
	response := s.discover.resolveFlightAlgorithms(r.Context(), coin, deviceType, minerKind, minerID, installed)
	writeJSON(w, http.StatusOK, response)
}

func (s *HTTPServer) discoverNetworks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"networks": localScanNetworks()})
}

func (s *HTTPServer) discoverScanStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.annotateScanStatus(s.scanner.status()))
}

func (s *HTTPServer) discoverStartScan(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CIDR string `json:"cidr"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		http.Error(w, "send a JSON body containing a cidr value", http.StatusBadRequest)
		return
	}
	cidr := strings.TrimSpace(request.CIDR)
	if cidr == "" {
		networks := localScanNetworks()
		if len(networks) == 0 {
			http.Error(w, "no private IPv4 network was detected on this controller", http.StatusBadRequest)
			return
		}
		cidr = networks[0].CIDR
	}
	if err := s.scanner.start(cidr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, s.annotateScanStatus(s.scanner.status()))
}

func (s *HTTPServer) discoverStopScan(w http.ResponseWriter, _ *http.Request) {
	s.scanner.stop()
	writeJSON(w, http.StatusOK, s.annotateScanStatus(s.scanner.status()))
}

// annotateScanStatus marks discovered addresses that already belong to an
// enrolled rig so operators can tell new hardware from known hardware.
func (s *HTTPServer) annotateScanStatus(status scanStatus) scanStatus {
	rigs := s.store.ListRigs(time.Now().UTC())
	byAddress := make(map[string]string, len(rigs))
	for _, rig := range rigs {
		address := strings.TrimSpace(rig.IPAddress)
		if address != "" {
			byAddress[address] = rig.ID
		}
	}
	for index := range status.Devices {
		rigID, ok := byAddress[status.Devices[index].IP]
		if !ok {
			continue
		}
		device := &status.Devices[index]
		device.EnrolledRigID = rigID
		// An enrolled rig is known hardware, so it never needs fingerprint guessing.
		device.IsMiner = true
		device.Score = 100
		device.Confidence = "confirmed"
		device.Reasons = append(device.Reasons, "Already enrolled in Miner Dash")
		if device.Platform == "" {
			device.Platform = "Miner Dash"
		}
	}
	return status
}
