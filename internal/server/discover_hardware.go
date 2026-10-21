package server

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type calcBenchmark struct {
	Algorithm string  `json:"algorithm"`
	Hashrate  float64 `json:"hashrate"`
	Unit      string  `json:"unit"`
	Watts     float64 `json:"watts"`
}

type calcHardware struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Vendor     string          `json:"vendor"`
	Name       string          `json:"name"`
	Benchmarks []calcBenchmark `json:"benchmarks,omitempty"`
}

type hardwareDetailCache struct {
	expires    time.Time
	benchmarks []calcBenchmark
}

var (
	wtmGPUListPattern           = regexp.MustCompile(`<a class="manufacturer--([a-z]+)[^"]*" href="/gpus/(\d+)-[^"]*"[^>]*>([^<]+)</a>`)
	wtmGPUBenchmarkPattern      = regexp.MustCompile(`(?s)<a href="/coins/\d+-[^"]*/gpus/\d+[^"]*">([^<]+)</a><br>\s*([^<]+?)\s*<span class="text-body-secondary fs-6">\s*([^<]+?)\s*</span>`)
	wtmASICPattern              = regexp.MustCompile(`(?s)id="table_row_asic_(\d+)"[^>]*>.*?<a class="stretched-link" href="/asics/[^"]*">\s*([^<]+?)\s*</a>.*?<td class="text-end table-[^"]*">\s*([^<]+?)\s*<div>\s*([^<]+?)\s*</div>`)
	hashrateCPUListPattern      = regexp.MustCompile(`(?s)onclick="window\.location\.href='/cpus/([^']+)'".*?<div class='brand'>([^<]+)</div><div class='model'[^>]*>([^<]+)</div>`)
	hashrateCPUBenchmarkPattern = regexp.MustCompile(`(?si)<li><a href='/cpus/[^']+/[^']+'>.*?<div class='name'>.*?</div><div class='estimatesDescription'>([^<]+)</div>.*?<div class='estimates'>([\d.,]+)\s*([kmgtpe]?)h/s</div><div class='estimatesDescription'>Hashrate.*?<div class='estimatesDescription'>Power</div><div class='estimates'>([\d.,]+)\s*w</div>`)
	benchmarkValuePattern       = regexp.MustCompile(`(?i)([\d.,]+)\s*([kmgtpe]?)(h|sol)\/s\s*@\s*([\d.,]+)\s*w`)
)

func (p *discoverProvider) calcHardware(ctx context.Context) ([]calcHardware, error) {
	return p.hardware.get(ctx, func(ctx context.Context) ([]calcHardware, error) {
		type result struct {
			items []calcHardware
			err   error
		}
		results := make(chan result, 3)
		load := func(rawURL string, parse func(string) []calcHardware) {
			body, err := p.fetchText(ctx, rawURL, "", 30*time.Second, 32<<20)
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{items: parse(body)}
		}
		go load(whatToMineGPUs, parseWhatToMineGPUList)
		go load(whatToMineASICs, parseWhatToMineASICList)
		go load(hashrateNoCPUs, parseHashrateCPUList)

		hardware := make([]calcHardware, 0, 500)
		var firstErr error
		for range 3 {
			result := <-results
			if result.err != nil && firstErr == nil {
				firstErr = result.err
			}
			hardware = append(hardware, result.items...)
		}
		go p.refreshCMPBenchmarks(ctx)
		hardware = append(hardware, p.cmpHardwareCatalog()...)
		if len(hardware) == 0 {
			return nil, fmt.Errorf("load mining hardware: %w", firstErr)
		}
		sort.SliceStable(hardware, func(i, j int) bool {
			if hardware[i].Type != hardware[j].Type {
				return hardware[i].Type < hardware[j].Type
			}
			if hardware[i].Vendor != hardware[j].Vendor {
				return hardware[i].Vendor < hardware[j].Vendor
			}
			return hardware[i].Name < hardware[j].Name
		})
		return hardware, nil
	})
}

var minerstatCMPPattern = regexp.MustCompile(`(?i)/algorithm/([^\s]+?)([\d.,]+)\s*([kmgtpe]?)h/s([\d.,]+)\s*w`)

var minerstatCMPURLs = map[string]string{
	"cmp-30hx":       "https://www.minerstat.com/hardware/nvidia-cmp-30hx",
	"cmp-40hx":       "https://www.minerstat.com/hardware/nvidia-cmp-40hx",
	"cmp-50hx":       "https://www.minerstat.com/hardware/nvidia-cmp-50hx",
	"cmp-100-210":    "https://www.minerstat.com/hardware/nvidia-cmp-100-210",
	"cmp-70hx":       "https://www.minerstat.com/hardware/nvidia-cmp-70hx",
	"cmp-90hx":       "https://www.minerstat.com/hardware/nvidia-cmp-90hx",
	"cmp-170hx-8gb":  "https://www.minerstat.com/hardware/nvidia-cmp-170hx",
	"cmp-170hx-10gb": "https://www.minerstat.com/hardware/nvidia-cmp-170hx",
}

func parseMinerstatCMPBenchmarks(body string) []calcBenchmark {
	seen := make(map[string]calcBenchmark)
	for _, match := range minerstatCMPPattern.FindAllStringSubmatch(body, -1) {
		value, err := strconv.ParseFloat(strings.ReplaceAll(match[2], ",", ""), 64)
		if err != nil {
			continue
		}
		watts, err := strconv.ParseFloat(strings.ReplaceAll(match[4], ",", ""), 64)
		if err != nil || watts <= 0 {
			continue
		}
		algorithm := strings.TrimSpace(match[1])
		seen[normalizeHardwareAlgorithm(algorithm)] = calcBenchmark{Algorithm: algorithm, Hashrate: value * hashratePrefix(match[3]), Unit: "H/s", Watts: watts}
	}
	return sortedBenchmarks(seen)
}

func (p *discoverProvider) refreshCMPBenchmarks(ctx context.Context) {
	p.hardwareMu.Lock()
	p.loadCMPBenchmarks()
	p.hardwareMu.Unlock()
	changed := false
	for id, rawURL := range minerstatCMPURLs {
		p.hardwareMu.Lock()
		_, exists := p.cmpBenchmarks[id]
		p.hardwareMu.Unlock()
		if exists {
			continue
		}
		body, err := p.fetchText(ctx, rawURL, "https://www.minerstat.com/hardware", 20*time.Second, 2<<20)
		if err != nil {
			continue
		}
		benchmarks := parseMinerstatCMPBenchmarks(body)
		if len(benchmarks) == 0 {
			continue
		}
		p.hardwareMu.Lock()
		p.cmpBenchmarks[id] = benchmarks
		p.hardwareMu.Unlock()
		changed = true
	}
	if changed {
		p.hardwareMu.Lock()
		p.saveCMPBenchmarks()
		p.hardwareMu.Unlock()
	}
}

func cmpHardwareCatalog() []calcHardware {
	return []calcHardware{
		{ID: "cmp-30hx", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 30HX", Benchmarks: cmpBenchmarks(30, 45)},
		{ID: "cmp-40hx", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 40HX", Benchmarks: cmpBenchmarks(45, 60)},
		{ID: "cmp-50hx", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 50HX", Benchmarks: cmpBenchmarks(52, 75)},
		{ID: "cmp-100-210", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 100-210 (100HX)", Benchmarks: cmpBenchmarks(75, 150)},
		{ID: "cmp-70hx", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 70HX", Benchmarks: cmpBenchmarks(90, 125)},
		{ID: "cmp-90hx", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 90HX", Benchmarks: cmpBenchmarks(105, 150)},
		{ID: "cmp-170hx-8gb", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 170HX (8 GB)", Benchmarks: cmpBenchmarks(155, 200)},
		{ID: "cmp-170hx-10gb", Type: "gpu", Vendor: "NVIDIA", Name: "NVIDIA CMP 170HX (10 GB)", Benchmarks: cmpBenchmarks(165, 210)},
	}
}

func (p *discoverProvider) cmpHardwareCatalog() []calcHardware {
	p.hardwareMu.Lock()
	defer p.hardwareMu.Unlock()
	p.loadCMPBenchmarks()
	result := cmpHardwareCatalog()
	for index := range result {
		if benchmarks := p.cmpBenchmarks[result[index].ID]; len(benchmarks) > 0 {
			result[index].Benchmarks = benchmarks
		}
	}
	return result
}
func cmpBenchmarks(ethashMH, watts float64) []calcBenchmark {
	return []calcBenchmark{
		{Algorithm: "ethash", Hashrate: ethashMH * 1e6, Unit: "H/s", Watts: watts},
		{Algorithm: "etchash", Hashrate: ethashMH * 1e6, Unit: "H/s", Watts: watts},
		{Algorithm: "kawpow", Hashrate: ethashMH * 0.42 * 1e6, Unit: "H/s", Watts: watts},
		{Algorithm: "autolykos2", Hashrate: ethashMH * 0.75 * 1e6, Unit: "H/s", Watts: watts},
		{Algorithm: "progpow", Hashrate: ethashMH * 0.55 * 1e6, Unit: "H/s", Watts: watts},
		{Algorithm: "progpowz", Hashrate: ethashMH * 0.55 * 1e6, Unit: "H/s", Watts: watts},
	}
}

func (p *discoverProvider) calcHardwareBenchmarks(ctx context.Context, hardwareType, id string) ([]calcBenchmark, error) {
	hardwareType = strings.ToLower(strings.TrimSpace(hardwareType))
	id = strings.TrimSpace(id)
	key := hardwareType + ":" + id
	p.hardwareMu.Lock()
	if cached, ok := p.hardwareDetails[key]; ok && time.Now().Before(cached.expires) {
		p.hardwareMu.Unlock()
		return cached.benchmarks, nil
	}
	p.hardwareMu.Unlock()

	var (
		rawURL string
		parse  func(string) []calcBenchmark
	)
	switch hardwareType {
	case "gpu":
		for _, device := range p.cmpHardwareCatalog() {
			if device.ID == id {
				return device.Benchmarks, nil
			}
		}
		if _, err := strconv.Atoi(id); err != nil {
			return nil, fmt.Errorf("invalid GPU id")
		}
		rawURL = whatToMineGPUs + "/" + id
		parse = parseWhatToMineGPUBenchmarks
	case "cpu":
		if !validHardwareSlug(id) {
			return nil, fmt.Errorf("invalid CPU id")
		}
		rawURL = hashrateNoCPUs + "/" + id
		parse = parseHashrateCPUBenchmarks
	case "asic":
		hardware, err := p.calcHardware(ctx)
		if err != nil {
			return nil, err
		}
		for _, device := range hardware {
			if device.Type == "asic" && device.ID == id {
				return device.Benchmarks, nil
			}
		}
		return nil, fmt.Errorf("ASIC not found")
	default:
		return nil, fmt.Errorf("unsupported hardware type")
	}

	body, err := p.fetchText(ctx, rawURL, "", 25*time.Second, 16<<20)
	if err != nil {
		return nil, err
	}
	benchmarks := parse(body)
	if len(benchmarks) == 0 {
		return nil, fmt.Errorf("no benchmarks found for this %s", hardwareType)
	}
	p.hardwareMu.Lock()
	p.hardwareDetails[key] = hardwareDetailCache{expires: time.Now().Add(time.Hour), benchmarks: benchmarks}
	p.hardwareMu.Unlock()
	return benchmarks, nil
}

func parseWhatToMineGPUList(body string) []calcHardware {
	seen := make(map[string]bool)
	hardware := make([]calcHardware, 0)
	for _, match := range wtmGPUListPattern.FindAllStringSubmatch(body, -1) {
		if seen[match[2]] {
			continue
		}
		seen[match[2]] = true
		vendor := strings.ToUpper(match[1])
		if vendor == "AMD" {
			vendor = "AMD"
		}
		hardware = append(hardware, calcHardware{ID: match[2], Type: "gpu", Vendor: vendor, Name: decodeHardwareText(match[3])})
	}
	return hardware
}

func parseWhatToMineGPUBenchmarks(body string) []calcBenchmark {
	byAlgorithm := make(map[string]calcBenchmark)
	for _, match := range wtmGPUBenchmarkPattern.FindAllStringSubmatch(body, -1) {
		benchmark, ok := parseBenchmarkValue(match[3])
		if !ok {
			continue
		}
		benchmark.Algorithm = decodeHardwareText(match[2])
		if _, exists := byAlgorithm[normalizeHardwareAlgorithm(benchmark.Algorithm)]; !exists {
			byAlgorithm[normalizeHardwareAlgorithm(benchmark.Algorithm)] = benchmark
		}
	}
	return sortedBenchmarks(byAlgorithm)
}

func parseWhatToMineASICList(body string) []calcHardware {
	hardware := make([]calcHardware, 0)
	for _, match := range wtmASICPattern.FindAllStringSubmatch(body, -1) {
		benchmark, ok := parseBenchmarkValue(match[3])
		if !ok {
			continue
		}
		name := decodeHardwareText(match[2])
		benchmark.Algorithm = decodeHardwareText(match[4])
		vendor := strings.Fields(name)
		vendorName := ""
		if len(vendor) > 0 {
			vendorName = vendor[0]
		}
		hardware = append(hardware, calcHardware{
			ID: match[1], Type: "asic", Vendor: vendorName, Name: name,
			Benchmarks: []calcBenchmark{benchmark},
		})
	}
	return hardware
}

func parseHashrateCPUList(body string) []calcHardware {
	seen := make(map[string]bool)
	hardware := make([]calcHardware, 0)
	for _, match := range hashrateCPUListPattern.FindAllStringSubmatch(body, -1) {
		if seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		hardware = append(hardware, calcHardware{
			ID: match[1], Type: "cpu", Vendor: decodeHardwareText(match[2]), Name: decodeHardwareText(match[3]),
		})
	}
	return hardware
}

func parseHashrateCPUBenchmarks(body string) []calcBenchmark {
	byAlgorithm := make(map[string]calcBenchmark)
	for _, match := range hashrateCPUBenchmarkPattern.FindAllStringSubmatch(body, -1) {
		value, err := strconv.ParseFloat(strings.ReplaceAll(match[2], ",", ""), 64)
		if err != nil {
			continue
		}
		watts, _ := strconv.ParseFloat(strings.ReplaceAll(match[4], ",", ""), 64)
		benchmark := calcBenchmark{
			Algorithm: decodeHardwareText(match[1]),
			Hashrate:  value * hashratePrefix(match[3]),
			Unit:      "H/s",
			Watts:     watts,
		}
		key := normalizeHardwareAlgorithm(benchmark.Algorithm)
		if _, exists := byAlgorithm[key]; !exists {
			byAlgorithm[key] = benchmark
		}
	}
	return sortedBenchmarks(byAlgorithm)
}

func parseBenchmarkValue(text string) (calcBenchmark, bool) {
	match := benchmarkValuePattern.FindStringSubmatch(decodeHardwareText(text))
	if len(match) != 5 {
		return calcBenchmark{}, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
	if err != nil {
		return calcBenchmark{}, false
	}
	watts, _ := strconv.ParseFloat(strings.ReplaceAll(match[4], ",", ""), 64)
	unit := "H/s"
	if strings.EqualFold(match[3], "sol") {
		unit = "Sol/s"
	}
	return calcBenchmark{Hashrate: value * hashratePrefix(match[2]), Unit: unit, Watts: watts}, true
}

func hashratePrefix(prefix string) float64 {
	switch strings.ToLower(prefix) {
	case "k":
		return 1e3
	case "m":
		return 1e6
	case "g":
		return 1e9
	case "t":
		return 1e12
	case "p":
		return 1e15
	case "e":
		return 1e18
	default:
		return 1
	}
}

func sortedBenchmarks(byAlgorithm map[string]calcBenchmark) []calcBenchmark {
	benchmarks := make([]calcBenchmark, 0, len(byAlgorithm))
	for _, benchmark := range byAlgorithm {
		benchmarks = append(benchmarks, benchmark)
	}
	sort.SliceStable(benchmarks, func(i, j int) bool { return benchmarks[i].Algorithm < benchmarks[j].Algorithm })
	return benchmarks
}

func normalizeHardwareAlgorithm(value string) string {
	return regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(value), "")
}

func decodeHardwareText(value string) string {
	return strings.TrimSpace(html.UnescapeString(cleanHTMLText(value)))
}

func validHardwareSlug(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
