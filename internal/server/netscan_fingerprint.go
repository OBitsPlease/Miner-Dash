package server

import (
	"regexp"
	"sort"
	"strings"
)

type scanDetection struct {
	Platform    string
	Model       string
	Score       int
	Confidence  string
	Reasons     []string
	MatchedPort int
}

type fingerprintRule struct {
	Platform string
	Weight   int
	Reason   string
	// Port restricts a page rule to a single port; zero means any page.
	Port int
	// Source selects the evidence the pattern is matched against.
	Source  string
	Pattern *regexp.Regexp
	// Model captures a model name from the first named or numbered group.
	Model bool
}

const (
	evidencePage     = "page"
	evidenceHostname = "hostname"
	evidenceBanner   = "banner"
	evidencePort     = "port"
)

var builtInFingerprints = []fingerprintRule{
	{Platform: "Jasminer", Weight: 60, Reason: "Web page identifies Jasminer", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)jasminer`)},
	{Platform: "Jasminer", Weight: 20, Reason: "Jasminer model found on web page", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)jasminer[\s_-]*(x\d+(?:-[a-z0-9]+)?)`), Model: true},
	{Platform: "Jasminer", Weight: 30, Reason: "Sunlune firmware signature", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)sunlune`)},
	{Platform: "Jasminer", Weight: 50, Reason: "Hostname contains Jasminer", Source: evidenceHostname, Pattern: regexp.MustCompile(`(?i)jasminer`)},

	{Platform: "VolcMiner", Weight: 60, Reason: "Web page identifies VolcMiner", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)volc\s*miner|火山矿机`)},
	{Platform: "VolcMiner", Weight: 20, Reason: "VolcMiner model found on web page", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)volc[\s_-]*miner[\s_-]*(d\d+[a-z0-9-]*)`), Model: true},
	{Platform: "VolcMiner", Weight: 50, Reason: "Hostname contains VolcMiner", Source: evidenceHostname, Pattern: regexp.MustCompile(`(?i)volcminer`)},

	{Platform: "HiveOS", Weight: 60, Reason: "Web page identifies Hive OS", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)hive\s*os|hiveon`)},
	{Platform: "HiveOS", Weight: 35, Reason: "Hive shell port 4200 open", Source: evidencePort, Port: 4200},
	{Platform: "HiveOS", Weight: 35, Reason: "Shell In A Box running on port 4200", Source: evidencePage, Port: 4200, Pattern: regexp.MustCompile(`(?i)shell in a box`)},
	{Platform: "HiveOS", Weight: 20, Reason: "Hostname contains hive", Source: evidenceHostname, Pattern: regexp.MustCompile(`(?i)hive`)},

	{Platform: "Miner Dash", Weight: 60, Reason: "Web page identifies Miner Dash", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)miner\s*dash(?:board)?`)},
	{Platform: "Miner Dash", Weight: 40, Reason: "Hostname contains Miner Dash", Source: evidenceHostname, Pattern: regexp.MustCompile(`(?i)miner[\s_-]*dash`)},

	{Platform: "Mining rig", Weight: 50, Reason: "Miner API returned a known banner", Source: evidenceBanner, Pattern: regexp.MustCompile(`(?i)cgminer|bmminer|sgminer|bfgminer|bosminer|luxminer|btminer|"api"\s*:`)},
	{Platform: "Mining rig", Weight: 15, Reason: "Miner API port 4028 open", Source: evidencePort, Port: 4028},
	{Platform: "Mining rig", Weight: 20, Reason: "Web page contains mining terminology", Source: evidencePage, Pattern: regexp.MustCompile(`(?i)hash[\s_-]?rate|\b(?:th|gh|mh)/s\b|stratum|mining[\s_-]*pool`)},
	{Platform: "Mining rig", Weight: 15, Reason: "Hostname looks like a rig", Source: evidenceHostname, Pattern: regexp.MustCompile(`(?i)\bminer\b|\basic\b|^(?:rig|worker)[-_]?\d*`)},
}

func fingerprintDevice(hostname string, openPorts []int, pages map[int]scanPage, banner string) scanDetection {
	scores := make(map[string]int)
	reasons := make(map[string][]string)
	models := make(map[string]string)
	matchedPorts := make(map[string]int)
	lowerHost := strings.ToLower(hostname)

	for _, rule := range builtInFingerprints {
		matched, model, port := evaluateFingerprint(rule, lowerHost, openPorts, pages, banner)
		if !matched {
			continue
		}
		scores[rule.Platform] += rule.Weight
		reasons[rule.Platform] = append(reasons[rule.Platform], rule.Reason)
		if model != "" && models[rule.Platform] == "" {
			models[rule.Platform] = model
		}
		if port > 0 && matchedPorts[rule.Platform] == 0 {
			matchedPorts[rule.Platform] = port
		}
	}
	if len(scores) == 0 {
		return scanDetection{Confidence: "unknown", Reasons: []string{}}
	}
	platforms := make([]string, 0, len(scores))
	for platform := range scores {
		platforms = append(platforms, platform)
	}
	// A specific platform outranks the generic rig rule at equal scores.
	sort.SliceStable(platforms, func(i, j int) bool {
		if scores[platforms[i]] != scores[platforms[j]] {
			return scores[platforms[i]] > scores[platforms[j]]
		}
		if (platforms[i] == "Mining rig") != (platforms[j] == "Mining rig") {
			return platforms[j] == "Mining rig"
		}
		return platforms[i] < platforms[j]
	})
	best := platforms[0]
	score := scores[best]
	if score > 100 {
		score = 100
	}
	return scanDetection{
		Platform:    best,
		Model:       models[best],
		Score:       score,
		Confidence:  scanConfidence(score),
		Reasons:     reasons[best],
		MatchedPort: matchedPorts[best],
	}
}

func evaluateFingerprint(rule fingerprintRule, hostname string, openPorts []int, pages map[int]scanPage, banner string) (bool, string, int) {
	switch rule.Source {
	case evidencePort:
		return containsInt(openPorts, rule.Port), "", rule.Port
	case evidenceHostname:
		if hostname == "" || !rule.Pattern.MatchString(hostname) {
			return false, "", 0
		}
		return true, "", 0
	case evidenceBanner:
		if banner == "" || !rule.Pattern.MatchString(banner) {
			return false, "", 0
		}
		return true, "", 4028
	case evidencePage:
		ports := make([]int, 0, len(pages))
		for port := range pages {
			if rule.Port != 0 && port != rule.Port {
				continue
			}
			ports = append(ports, port)
		}
		sort.Ints(ports)
		for _, port := range ports {
			match := rule.Pattern.FindStringSubmatch(pages[port].Body)
			if match == nil {
				continue
			}
			model := ""
			if rule.Model && len(match) > 1 {
				model = strings.ToUpper(strings.TrimSpace(match[1]))
			}
			return true, model, port
		}
	}
	return false, "", 0
}

func scanConfidence(score int) string {
	switch {
	case score >= 90:
		return "confirmed"
	case score >= 60:
		return "likely"
	case score >= 30:
		return "possible"
	default:
		return "unknown"
	}
}
