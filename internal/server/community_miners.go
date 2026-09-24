package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"minerdash/internal/protocol"
)

const (
	communityMinerCatalogURL = "https://raw.githubusercontent.com/OBitsPlease/MinerDash-Custom-Miners/main/catalog.json"
	communityMinerRepository = "https://github.com/OBitsPlease/MinerDash-Custom-Miners"
)

var communityMinerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)

type communityMinerRegistry struct {
	mu        sync.Mutex
	client    *http.Client
	url       string
	cached    protocol.CommunityMinerCatalog
	checkedAt time.Time
}

func newCommunityMinerRegistry() *communityMinerRegistry {
	return &communityMinerRegistry{
		client: &http.Client{Timeout: 15 * time.Second},
		url:    communityMinerCatalogURL,
	}
}

func (registry *communityMinerRegistry) catalog(ctx context.Context) protocol.CommunityMinerCatalog {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if !registry.checkedAt.IsZero() && time.Since(registry.checkedAt) < releaseCacheDuration {
		return registry.cached
	}
	result := protocol.CommunityMinerCatalog{SchemaVersion: 1, RepositoryURL: communityMinerRepository, Miners: []protocol.CommunityMiner{}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, registry.url, nil)
	if err != nil {
		result.Error = err.Error()
	} else {
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "MinerDash/0.1")
		response, requestErr := registry.client.Do(request)
		if requestErr != nil {
			result.Error = requestErr.Error()
		} else {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				result.Error = fmt.Sprintf("community catalog returned %s", response.Status)
			} else if decodeErr := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); decodeErr != nil {
				result.Error = "decode community catalog: " + decodeErr.Error()
			} else if validateErr := validateCommunityMinerCatalog(result); validateErr != nil {
				result.Miners = []protocol.CommunityMiner{}
				result.Error = validateErr.Error()
			}
		}
	}
	result.RepositoryURL = communityMinerRepository
	registry.cached = result
	registry.checkedAt = time.Now().UTC()
	return result
}

func validateCommunityMinerCatalog(catalog protocol.CommunityMinerCatalog) error {
	if catalog.SchemaVersion != 1 {
		return fmt.Errorf("unsupported community catalog schema version %d", catalog.SchemaVersion)
	}
	seen := make(map[string]bool)
	for _, miner := range catalog.Miners {
		if !communityMinerIDPattern.MatchString(miner.ID) || strings.TrimSpace(miner.Name) == "" || strings.TrimSpace(miner.Version) == "" {
			return errors.New("community catalog contains invalid miner identity")
		}
		if seen[miner.ID] {
			return fmt.Errorf("community catalog contains duplicate miner %q", miner.ID)
		}
		seen[miner.ID] = true
		if strings.TrimSpace(miner.RecommendedAlgorithm) == "" || filepathBase(miner.ExecutableName) != miner.ExecutableName {
			return fmt.Errorf("community miner %q has invalid executable or algorithm", miner.ID)
		}
		if miner.PackageFormat != "" && miner.PackageFormat != "binary" && miner.PackageFormat != "tar.gz" {
			return fmt.Errorf("community miner %q has invalid package format", miner.ID)
		}
		if miner.PackageFormat == "tar.gz" && !validBundlePath(miner.EntryPoint) {
			return fmt.Errorf("community miner %q has invalid bundle entry point", miner.ID)
		}
		if miner.PackageFormat == "tar.gz" && (len(miner.PackageSHA256) != 64 || !isHex(strings.ToLower(miner.PackageSHA256))) {
			return fmt.Errorf("community miner %q has invalid package SHA-256", miner.ID)
		}
		if len(miner.ExecutableSHA256) != 64 || !isHex(strings.ToLower(miner.ExecutableSHA256)) {
			return fmt.Errorf("community miner %q has invalid executable SHA-256", miner.ID)
		}
		parsed, err := url.Parse(miner.PackageURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return fmt.Errorf("community miner %q has invalid package URL", miner.ID)
		}
	}
	return nil
}

func filepathBase(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

func (s *HTTPServer) communityMiners(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.community.catalog(r.Context()))
}

func (s *HTTPServer) installCommunityMiner(w http.ResponseWriter, r *http.Request) {
	catalog := s.community.catalog(r.Context())
	if catalog.Error != "" {
		writeError(w, errors.New(catalog.Error))
		return
	}
	var selected *protocol.CommunityMiner
	for index := range catalog.Miners {
		if catalog.Miners[index].ID == r.PathValue("minerID") {
			selected = &catalog.Miners[index]
			break
		}
	}
	if selected == nil {
		writeError(w, ErrNotFound)
		return
	}
	miner, err := s.store.ImportCustomMiner(r.Context(), protocol.CustomMinerImport{
		CatalogID: selected.ID,
		Name:      selected.Name, Version: selected.Version, Algorithm: selected.RecommendedAlgorithm,
		URL: selected.PackageURL, BinaryName: selected.ExecutableName,
		PackageFormat: selected.PackageFormat, EntryPoint: selected.EntryPoint,
		Environment: selected.Environment, StatsType: selected.StatsType, StatsURL: selected.StatsURL,
		ExpectedBinarySHA256:  selected.ExecutableSHA256,
		ExpectedPackageSHA256: selected.PackageSHA256,
		DefaultArguments:      append([]string(nil), selected.DefaultArguments...),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, miner)
}
