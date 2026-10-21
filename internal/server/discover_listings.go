package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type newListing struct {
	Name       string  `json:"name"`
	Title      string  `json:"title"`
	Symbol     string  `json:"symbol"`
	Thread     string  `json:"thread"`
	Listed     string  `json:"listed"`
	ListedAt   string  `json:"listed_at"`
	Algo       string  `json:"algo"`
	AlgoSource string  `json:"algo_source"`
	Price      float64 `json:"price"`
	Website    string  `json:"website"`
	GitHub     string  `json:"github"`
	Explorer   string  `json:"explorer"`
	PoolsURL   string  `json:"pools_url"`
	Source     string  `json:"source"`
}

const (
	bitcointalkMaxThreads = 160
	bitcointalkWorkers    = 6
	bitcointalkRecentDays = 14
)

var (
	bitcointalkTopicPattern = regexp.MustCompile(`<span id="msg_\d+"><a href="(https://bitcointalk\.org/index\.php\?topic=\d+\.0)">(.*?)</a></span>`)
	bitcointalkAnnPattern   = regexp.MustCompile(`(?i)\[(?:ANN|RE-ANN)\]`)
	threadPostPattern       = regexp.MustCompile(`(?s)<div class="post">(.*?)</div>`)
	threadSmallTextPattern  = regexp.MustCompile(`(?s)class="smalltext"[^>]*>(.*?)</div>`)
	threadLinkPattern       = regexp.MustCompile(`(?i)href="(https?://[^"]+)"`)
	explicitAlgoPattern     = regexp.MustCompile(`(?i)\b(?:algorithm|algo)\b\s*[:\-]?\s*([A-Za-z0-9\+\-/ ]{2,24})`)
	listingSymbolPattern    = regexp.MustCompile(`\(([A-Z0-9]{2,10})\)|\$([A-Z0-9]{2,10})\b`)
	listingTagPrefixPattern = regexp.MustCompile(`^(?:\s*\[[^\]]*\])+`)
	listingSymbolPrefix     = regexp.MustCompile(`^\(([A-Za-z0-9]{2,10})\)\s*`)
	bitcointalkDateFormats  = []string{"January 2, 2006, 03:04:05 PM", "January 02, 2006, 03:04:05 PM"}
)

var listingAlgoKeywords = []struct {
	pattern *regexp.Regexp
	algo    string
}{
	{regexp.MustCompile(`(?i)\brandom\s*-?\s*x\b`), "RandomX"},
	{regexp.MustCompile(`(?i)\bkaw\s*-?\s*pow\b`), "KawPow"},
	{regexp.MustCompile(`(?i)\bghost\s*-?\s*rider\b`), "GhostRider"},
	{regexp.MustCompile(`(?i)\bverus\s*-?\s*hash\b`), "VerusHash"},
	{regexp.MustCompile(`(?i)\betchash\b`), "Etchash"},
	{regexp.MustCompile(`(?i)\bethash\b`), "Ethash"},
	{regexp.MustCompile(`(?i)\bfiro\s*-?\s*pow\b`), "FiroPow"},
	{regexp.MustCompile(`(?i)\bprog\s*-?\s*pow\b`), "ProgPow"},
	{regexp.MustCompile(`(?i)\bautolykos\b`), "Autolykos"},
	{regexp.MustCompile(`(?i)\boctopus\b`), "Octopus"},
	{regexp.MustCompile(`(?i)\bequihash\b`), "Equihash"},
	{regexp.MustCompile(`(?i)\byescrypt\b`), "Yescrypt"},
	{regexp.MustCompile(`(?i)\byespower\b`), "Yespower"},
	{regexp.MustCompile(`(?i)\bcryptonight\b`), "CryptoNight"},
	{regexp.MustCompile(`(?i)\bargon2d\b`), "Argon2d"},
	{regexp.MustCompile(`(?i)\blyra2\s*-?\s*(?:z|rev2|rev3)?\b`), "Lyra2"},
	{regexp.MustCompile(`(?i)\bx16rv2\b`), "X16Rv2"},
	{regexp.MustCompile(`(?i)\bx16r\b`), "X16R"},
	{regexp.MustCompile(`(?i)\bxevan\b`), "Xevan"},
	{regexp.MustCompile(`(?i)\bx11\b`), "X11"},
	{regexp.MustCompile(`(?i)\bblake3\b`), "Blake3"},
	{regexp.MustCompile(`(?i)\bblake2s\b`), "Blake2s"},
	{regexp.MustCompile(`(?i)\bsha\s*-?\s*256\b`), "SHA-256"},
	{regexp.MustCompile(`(?i)\bscrypt\b`), "Scrypt"},
}

var (
	listingIncludeKeywords = []string{"pow", "proof of work", "mineable", "mining", "miner", "cpu", "gpu", "asic", "phone"}
	listingExcludeKeywords = []string{"proof of stake", "proof-of-stake", "staking", "masternode", "pure pos"}
)

func (p *discoverProvider) newListings(ctx context.Context) ([]newListing, error) {
	return p.listings.get(ctx, func(ctx context.Context) ([]newListing, error) {
		now := time.Now().UTC()
		saved := p.loadSavedListings(now)
		board := ""
		threadBudget := bitcointalkMaxThreads
		if len(saved) > 0 {
			threadBudget = 40
		}
		for offset := 0; offset < threadBudget; offset += 40 {
			pageURL := bitcointalkBoard
			if offset > 0 {
				pageURL = "https://bitcointalk.org/index.php?board=159." + strconv.Itoa(offset)
			}
			page, err := p.fetchText(ctx, pageURL, bitcointalkBoard, 20*time.Second, 16<<20)
			if err != nil {
				if board == "" {
					if len(saved) > 0 {
						return saved, nil
					}
					return nil, err
				}
				break
			}
			board += "\n" + page
		}
		candidates := parseBitcointalkCandidates(board)
		if len(saved) > 0 {
			candidates = unseenListingCandidates(candidates, saved)
		}
		scanCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		listings := p.expandListings(scanCtx, candidates)
		rows, marketErr := p.miningPoolStatsRows(ctx)
		if marketErr == nil {
			annotateListingsWithMarket(listings, rows)
		}
		listings = mergeSavedListings(listings, saved)
		sort.SliceStable(listings, func(i, j int) bool { return listings[i].ListedAt > listings[j].ListedAt })
		p.saveListings(listings)
		return listings, nil
	})
}

func (p *discoverProvider) loadSavedListings(now time.Time) []newListing {
	if p.listingCachePath == "" {
		return nil
	}
	body, err := os.ReadFile(p.listingCachePath)
	if err != nil {
		return nil
	}
	var listings []newListing
	if json.Unmarshal(body, &listings) != nil {
		return nil
	}
	cutoff := now.Add(-bitcointalkRecentDays * 24 * time.Hour)
	recent := listings[:0]
	for _, listing := range listings {
		if !strings.EqualFold(strings.TrimSpace(listing.Source), "Bitcointalk") {
			continue
		}
		listedAt, err := time.Parse(time.RFC3339, listing.ListedAt)
		if err == nil && !listedAt.Before(cutoff) && !listedAt.After(now.Add(24*time.Hour)) {
			recent = append(recent, listing)
		}
	}
	return recent
}

func (p *discoverProvider) saveListings(listings []newListing) {
	if p.listingCachePath == "" || len(listings) == 0 {
		return
	}
	body, err := json.Marshal(listings)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p.listingCachePath), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(p.listingCachePath, body, 0o600)
}

func mergeSavedListings(current, saved []newListing) []newListing {
	seen := make(map[string]bool, len(current))
	for _, listing := range current {
		seen[listing.Thread+"|"+strings.ToUpper(listing.Symbol)+"|"+strings.ToLower(listing.Name)] = true
	}
	for _, listing := range saved {
		key := listing.Thread + "|" + strings.ToUpper(listing.Symbol) + "|" + strings.ToLower(listing.Name)
		if !seen[key] {
			current = append(current, listing)
			seen[key] = true
		}
	}
	return current
}

type listingCandidate struct {
	Title string
	URL   string
	Topic int
}

func parseBitcointalkCandidates(body string) []listingCandidate {
	candidates := make([]listingCandidate, 0, bitcointalkMaxThreads)
	seen := make(map[string]bool)
	for _, match := range bitcointalkTopicPattern.FindAllStringSubmatchIndex(body, -1) {
		if len(candidates) >= bitcointalkMaxThreads {
			break
		}
		threadURL := body[match[2]:match[3]]
		title := cleanHTMLText(body[match[4]:match[5]])
		if !bitcointalkAnnPattern.MatchString(title) || seen[threadURL] {
			continue
		}
		if isPinnedOrLockedTopic(body, match[0]) {
			continue
		}
		seen[threadURL] = true
		candidates = append(candidates, listingCandidate{Title: title, URL: threadURL, Topic: listingTopicID(threadURL)})
	}
	return candidates
}

func unseenListingCandidates(candidates []listingCandidate, saved []newListing) []listingCandidate {
	latestSavedTopic := 0
	for _, listing := range saved {
		topic := listingTopicID(listing.Thread)
		if topic > latestSavedTopic {
			latestSavedTopic = topic
		}
	}
	if latestSavedTopic == 0 {
		return candidates
	}
	unseen := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Topic > latestSavedTopic {
			unseen = append(unseen, candidate)
		}
	}
	return unseen
}

func listingTopicID(rawURL string) int {
	const marker = "topic="
	start := strings.Index(rawURL, marker)
	if start < 0 {
		return 0
	}
	start += len(marker)
	end := start
	for end < len(rawURL) && rawURL[end] >= '0' && rawURL[end] <= '9' {
		end++
	}
	topic, _ := strconv.Atoi(rawURL[start:end])
	return topic
}

// isPinnedOrLockedTopic inspects the topic-row icons rendered just before the
// subject link, which is how the board marks sticky and locked threads.
func isPinnedOrLockedTopic(body string, subjectIndex int) bool {
	start := subjectIndex - 1500
	if start < 0 {
		start = 0
	}
	window := strings.ToLower(body[start:subjectIndex])
	if cut := strings.LastIndex(window, "<tr"); cut > 0 {
		window = window[cut:]
	}
	return strings.Contains(window, "sticky") || strings.Contains(window, "locked")
}

func (p *discoverProvider) expandListings(ctx context.Context, candidates []listingCandidate) []newListing {
	results := make([]newListing, len(candidates))
	keep := make([]bool, len(candidates))
	queue := make(chan int)
	var waitGroup sync.WaitGroup
	for worker := 0; worker < bitcointalkWorkers; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range queue {
				listing, ok := p.buildListing(ctx, candidates[index])
				results[index] = listing
				keep[index] = ok
			}
		}()
	}
	for index := range candidates {
		select {
		case queue <- index:
		case <-ctx.Done():
			close(queue)
			waitGroup.Wait()
			return collectListings(results, keep)
		}
	}
	close(queue)
	waitGroup.Wait()
	return collectListings(results, keep)
}

func collectListings(results []newListing, keep []bool) []newListing {
	listings := make([]newListing, 0, len(results))
	for index, ok := range keep {
		if ok {
			listings = append(listings, results[index])
		}
	}
	return listings
}

func (p *discoverProvider) buildListing(ctx context.Context, candidate listingCandidate) (newListing, bool) {
	var body string
	for attempt := 0; attempt < 2; attempt++ {
		fetched, err := p.fetchText(ctx, candidate.URL, bitcointalkBoard, 15*time.Second, 4<<20)
		if err == nil {
			body = fetched
			break
		}
		if ctx.Err() != nil {
			return newListing{}, false
		}
	}
	if body == "" {
		return newListing{}, false
	}
	return buildListingFromThread(candidate, body, time.Now().UTC())
}

func buildListingFromThread(candidate listingCandidate, body string, now time.Time) (newListing, bool) {
	listing := newListing{Name: candidate.Title, Title: candidate.Title, Thread: candidate.URL, Source: "Bitcointalk"}
	postText := ""
	if match := threadPostPattern.FindStringSubmatch(body); len(match) == 2 {
		postText = cleanHTMLText(match[1])
		listing.Website, listing.GitHub, listing.Explorer = extractListingLinks(match[1])
	}

	listing.Listed = firstPostDate(body, now)
	listedAt, recent := listingDate(listing.Listed, now)
	if !recent {
		return newListing{}, false
	}
	listing.ListedAt = listedAt.Format(time.RFC3339)
	corpus := strings.ToLower(candidate.Title + " " + postText)
	if !listingIsProofOfWork(corpus) {
		return newListing{}, false
	}
	listing.Symbol = detectListingSymbol(candidate.Title)
	if name := listingCoinName(candidate.Title); name != "" {
		listing.Name = name
	}
	listing.Algo, listing.AlgoSource = detectListingAlgorithm(candidate.Title, postText)
	return listing, true
}

func firstPostDate(body string, now time.Time) string {
	post := threadPostPattern.FindStringIndex(body)
	if len(post) != 2 {
		return ""
	}
	headerStart := strings.LastIndex(body[:post[0]], "<tr")
	if headerStart < 0 {
		headerStart = 0
	}
	header := body[headerStart:post[0]]
	for _, match := range threadSmallTextPattern.FindAllStringSubmatch(header, -1) {
		if len(match) != 2 {
			continue
		}
		value := cleanHTMLText(match[1])
		if _, ok := parseListingTimestamp(value, now); ok {
			return value
		}
	}
	return ""
}

func listingIsProofOfWork(corpus string) bool {
	for _, keyword := range listingExcludeKeywords {
		if strings.Contains(corpus, keyword) {
			return false
		}
	}
	for _, keyword := range listingIncludeKeywords {
		if strings.Contains(corpus, keyword) {
			return true
		}
	}
	return false
}

func listingDate(value string, now time.Time) (time.Time, bool) {
	parsed, ok := parseListingTimestamp(value, now)
	if !ok {
		return time.Time{}, false
	}
	if now.Sub(parsed) > bitcointalkRecentDays*24*time.Hour {
		return parsed, false
	}
	return parsed, true
}

func parseListingTimestamp(value string, now time.Time) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	switch {
	case lower == "":
		return time.Time{}, false
	case strings.HasPrefix(lower, "today"):
		return now, true
	case strings.HasPrefix(lower, "yesterday"):
		return now.AddDate(0, 0, -1), true
	}
	for _, layout := range bitcointalkDateFormats {
		parsed, err := time.Parse(layout, trimmed)
		if err != nil {
			continue
		}
		return parsed, true
	}
	return time.Time{}, false
}

var listingAssetPattern = regexp.MustCompile(`(?i)\.(?:png|jpe?g|gif|webp|svg|ico|css|js|zip|exe|pdf)(?:\?|#|$)`)

func extractListingLinks(body string) (website, github, explorer string) {
	for _, match := range threadLinkPattern.FindAllStringSubmatch(body, -1) {
		link := match[1]
		lower := strings.ToLower(link)
		if strings.Contains(lower, "bitcointalk.org") || listingAssetPattern.MatchString(lower) {
			continue
		}
		switch {
		case github == "" && strings.Contains(lower, "github.com"):
			github = link
		case explorer == "" && (strings.Contains(lower, "explorer") || strings.Contains(lower, "chainz.cryptoid")):
			explorer = link
		case website == "" && !strings.Contains(lower, "github.com"):
			website = link
		}
	}
	return website, github, explorer
}

func detectListingSymbol(title string) string {
	for _, match := range listingSymbolPattern.FindAllStringSubmatch(title, -1) {
		for _, group := range match[1:] {
			candidate := strings.TrimSpace(group)
			if candidate == "" || candidate == "ANN" || candidate == "RE-ANN" {
				continue
			}
			return candidate
		}
	}
	return ""
}

func detectListingAlgorithm(title, post string) (string, string) {
	if match := explicitAlgoPattern.FindStringSubmatch(post); len(match) == 2 {
		if algo := normalizeListingAlgo(match[1]); algo != "" {
			return algo, "medium"
		}
	}
	corpus := title + " " + post
	for _, keyword := range listingAlgoKeywords {
		if keyword.pattern.MatchString(corpus) {
			return keyword.algo, "low"
		}
	}
	return "", "unknown"
}

func normalizeListingAlgo(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, keyword := range listingAlgoKeywords {
		if keyword.pattern.MatchString(trimmed) {
			return keyword.algo
		}
	}
	if len(trimmed) > 24 {
		return ""
	}
	return trimmed
}

// annotateListingsWithMarket upgrades algorithm confidence to "high" whenever the
// announced coin already exists in the MiningPoolStats directory.
func annotateListingsWithMarket(listings []newListing, rows []mpsRow) {
	bySymbol := make(map[string]mpsRow, len(rows))
	byName := make(map[string]mpsRow, len(rows))
	for _, row := range rows {
		symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if symbol != "" {
			bySymbol[symbol] = row
		}
		name := strings.ToLower(strings.TrimSpace(row.Name))
		if name != "" {
			byName[name] = row
		}
	}
	for index := range listings {
		listing := &listings[index]
		row, ok := bySymbol[strings.ToUpper(listing.Symbol)]
		if !ok {
			row, ok = byName[strings.ToLower(listingCoinName(listing.Name))]
		}
		if !ok {
			continue
		}
		if algo := strings.TrimSpace(row.Algo); algo != "" {
			listing.Algo = algo
			listing.AlgoSource = "high"
		}
		listing.Price = row.Price.float()
	}
}

// listingCoinName strips announcement tags, a leading "(SYM)" prefix, and any
// tagline so "[ANN][PoW] (TNR)Tenero | privacy coin" becomes "Tenero".
func listingCoinName(title string) string {
	name := listingTagPrefixPattern.ReplaceAllString(strings.TrimSpace(title), " ")
	name = bitcointalkAnnPattern.ReplaceAllString(name, " ")
	name = listingSymbolPrefix.ReplaceAllString(strings.TrimSpace(name), "")
	if cut := strings.IndexRune(name, '\uFFFD'); cut > 0 {
		name = name[:cut]
	}
	for _, separator := range []string{" - ", " – ", ":", "|"} {
		if cut := strings.Index(name, separator); cut > 0 {
			name = name[:cut]
		}
	}
	name = strings.TrimSpace(strings.Trim(strings.TrimSpace(name), "$"))
	if open := strings.Index(name, "("); open > 0 {
		name = strings.TrimSpace(name[:open])
	}
	return name
}
