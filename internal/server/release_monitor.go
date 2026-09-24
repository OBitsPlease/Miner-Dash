package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"minerdash/internal/protocol"
)

const releaseCacheDuration = time.Hour

type catalogRelease struct {
	Version     string
	URL         string
	Notes       string
	PublishedAt time.Time
	CheckedAt   time.Time
	Error       string
}

type catalogReleaseMonitor struct {
	mu      sync.Mutex
	client  *http.Client
	apiBase string
	cache   map[string]catalogRelease
}

func newCatalogReleaseMonitor() *catalogReleaseMonitor {
	return &catalogReleaseMonitor{
		client:  &http.Client{Timeout: 10 * time.Second},
		apiBase: "https://api.github.com/repos/",
		cache:   make(map[string]catalogRelease),
	}
}

func (m *catalogReleaseMonitor) enrich(ctx context.Context, status *protocol.RigOSStatus) {
	type result struct {
		index   int
		release catalogRelease
	}
	results := make(chan result, len(status.Miners))
	pending := 0
	for index, entry := range status.Miners {
		source, ok := catalogReleaseSources[entry.ID]
		if !ok {
			continue
		}
		pending++
		go func(index int, source catalogReleaseSource) {
			results <- result{index: index, release: m.latest(ctx, source)}
		}(index, source)
	}
	for range pending {
		item := <-results
		entry := &status.Miners[item.index]
		entry.LatestVersion = item.release.Version
		entry.LatestReleaseURL = item.release.URL
		entry.LatestReleaseNotes = item.release.Notes
		entry.LatestPublishedAt = item.release.PublishedAt
		entry.ReleaseCheckedAt = item.release.CheckedAt
		entry.ReleaseCheckError = item.release.Error
		entry.UpdateAvailable = entry.PackageReady && versionLess(entry.InstalledVersion, entry.LatestVersion)
	}
}

func (m *catalogReleaseMonitor) latest(ctx context.Context, source catalogReleaseSource) catalogRelease {
	now := time.Now().UTC()
	m.mu.Lock()
	cached, ok := m.cache[source.Repository]
	if ok && now.Sub(cached.CheckedAt) < releaseCacheDuration {
		m.mu.Unlock()
		return cached
	}
	m.mu.Unlock()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiBase+source.Repository+"/releases/latest", nil)
	if err != nil {
		return catalogRelease{CheckedAt: now, Error: err.Error()}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "MinerDash/0.1")
	response, err := m.client.Do(request)
	release := catalogRelease{CheckedAt: now}
	if err != nil {
		release.Error = err.Error()
	} else {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
			release.Error = fmt.Sprintf("GitHub returned %s: %s", response.Status, strings.TrimSpace(string(message)))
		} else {
			var payload struct {
				TagName     string    `json:"tag_name"`
				HTMLURL     string    `json:"html_url"`
				Body        string    `json:"body"`
				PublishedAt time.Time `json:"published_at"`
			}
			if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
				release.Error = "decode GitHub release: " + err.Error()
			} else {
				release.Version = strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")
				release.URL = payload.HTMLURL
				release.Notes = truncateReleaseNotes(payload.Body)
				release.PublishedAt = payload.PublishedAt
			}
		}
	}
	m.mu.Lock()
	m.cache[source.Repository] = release
	m.mu.Unlock()
	return release
}

func truncateReleaseNotes(notes string) string {
	notes = strings.TrimSpace(strings.ReplaceAll(notes, "\r\n", "\n"))
	const maximum = 4000
	if len(notes) <= maximum {
		return notes
	}
	return strings.TrimSpace(notes[:maximum]) + "\n..."
}

var versionNumberPattern = regexp.MustCompile(`\d+`)

func versionLess(installed, latest string) bool {
	if installed == "" || latest == "" {
		return false
	}
	left := versionNumberPattern.FindAllString(installed, -1)
	right := versionNumberPattern.FindAllString(latest, -1)
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	count := max(len(left), len(right))
	for index := range count {
		var leftValue, rightValue int
		if index < len(left) {
			leftValue, _ = strconv.Atoi(left[index])
		}
		if index < len(right) {
			rightValue, _ = strconv.Atoi(right[index])
		}
		if leftValue != rightValue {
			return leftValue < rightValue
		}
	}
	return false
}
