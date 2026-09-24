package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"minerdash/internal/buildinfo"
)

const applicationReleasesURL = "https://api.github.com/repos/OBitsPlease/Miner-Dash/releases"

type applicationUpdateStatus struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	Error           string `json:"error,omitempty"`
}

type applicationRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

type applicationUpdateMonitor struct {
	mu        sync.Mutex
	client    *http.Client
	url       string
	checkedAt time.Time
	release   applicationRelease
	err       string
}

func newApplicationUpdateMonitor() *applicationUpdateMonitor {
	return &applicationUpdateMonitor{
		client: &http.Client{Timeout: 20 * time.Second},
		url:    applicationReleasesURL,
	}
}

func (monitor *applicationUpdateMonitor) latest(ctx context.Context, refresh bool) (applicationRelease, error) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if !refresh && !monitor.checkedAt.IsZero() && time.Since(monitor.checkedAt) < releaseCacheDuration {
		if monitor.err != "" {
			return applicationRelease{}, errors.New(monitor.err)
		}
		return monitor.release, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, monitor.url+"?per_page=20", nil)
	if err != nil {
		return applicationRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "MinerDash/"+buildinfo.Version)
	response, err := monitor.client.Do(request)
	if err != nil {
		monitor.err = err.Error()
		monitor.checkedAt = time.Now()
		return applicationRelease{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("GitHub releases returned %s", response.Status)
		monitor.err = err.Error()
		monitor.checkedAt = time.Now()
		return applicationRelease{}, err
	}
	var releases []applicationRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&releases); err != nil {
		monitor.err = err.Error()
		monitor.checkedAt = time.Now()
		return applicationRelease{}, err
	}
	var selected applicationRelease
	for _, release := range releases {
		if release.Draft || strings.TrimSpace(release.TagName) == "" {
			continue
		}
		selected = release
		break
	}
	if selected.TagName == "" {
		err = errors.New("no Miner Dash releases are published")
		monitor.err = err.Error()
		monitor.checkedAt = time.Now()
		return applicationRelease{}, err
	}
	monitor.release = selected
	monitor.err = ""
	monitor.checkedAt = time.Now()
	return selected, nil
}

func (monitor *applicationUpdateMonitor) status(ctx context.Context) applicationUpdateStatus {
	status := applicationUpdateStatus{CurrentVersion: buildinfo.Version}
	release, err := monitor.latest(ctx, false)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.LatestVersion = strings.TrimPrefix(release.TagName, "v")
	status.ReleaseURL = release.HTMLURL
	status.PublishedAt = release.PublishedAt
	status.UpdateAvailable = applicationVersionLess(buildinfo.Version, status.LatestVersion)
	return status
}

func (s *HTTPServer) applicationUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.appUpdates.status(r.Context()))
}

func (s *HTTPServer) installApplicationUpdate(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "windows" {
		writeError(w, errors.New("automatic controller updates currently require Windows"))
		return
	}
	release, err := s.appUpdates.latest(r.Context(), true)
	if err != nil {
		writeError(w, err)
		return
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if !applicationVersionLess(buildinfo.Version, latest) {
		writeJSON(w, http.StatusOK, s.appUpdates.status(r.Context()))
		return
	}

	assets := make(map[string]string)
	for _, asset := range release.Assets {
		assets[asset.Name] = asset.URL
	}
	required := []string{"SHA256SUMS.json", "minerdash-server.exe", "minerdash-agent-amd64", "minerdash-agent-arm64"}
	for _, name := range required {
		if assets[name] == "" {
			writeError(w, fmt.Errorf("release %s is missing %s", release.TagName, name))
			return
		}
	}
	updateRoot := filepath.Join(filepath.Dir(s.store.path), "updates", latest)
	if err := os.MkdirAll(updateRoot, 0o700); err != nil {
		writeError(w, err)
		return
	}
	manifestPath := filepath.Join(updateRoot, "SHA256SUMS.json")
	if err := downloadReleaseAsset(r.Context(), s.appUpdates.client, assets["SHA256SUMS.json"], manifestPath, 2<<20, ""); err != nil {
		writeError(w, err)
		return
	}
	manifest, err := readReleaseManifest(manifestPath)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, name := range required[1:] {
		expected := manifest[name]
		if expected == "" {
			writeError(w, fmt.Errorf("release checksum manifest is missing %s", name))
			return
		}
		if err := downloadReleaseAsset(r.Context(), s.appUpdates.client, assets[name], filepath.Join(updateRoot, name), 250<<20, expected); err != nil {
			writeError(w, err)
			return
		}
	}
	executable, err := os.Executable()
	if err != nil {
		writeError(w, err)
		return
	}
	helper := filepath.Join(filepath.Dir(executable), "minerdash-updater.exe")
	if info, err := os.Stat(helper); err != nil || !info.Mode().IsRegular() {
		writeError(w, errors.New("automatic updater helper is not installed; download the latest installer from the release page"))
		return
	}
	command := exec.Command(helper,
		"-service", "MinerDash",
		"-server", executable,
		"-staged-server", filepath.Join(updateRoot, "minerdash-server.exe"),
		"-agent-directory", s.store.agentReleaseDir,
		"-staged-amd64", filepath.Join(updateRoot, "minerdash-agent-amd64"),
		"-staged-arm64", filepath.Join(updateRoot, "minerdash-agent-arm64"),
		"-health", "https://localhost:8443/healthz",
		"-log", filepath.Join(updateRoot, "update.log"),
	)
	if err := command.Start(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "updating",
		"version": latest,
	})
}

func applicationVersionLess(current, latest string) bool {
	parse := func(value string) ([3]int, string, bool) {
		var core [3]int
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		parts := strings.SplitN(value, "-", 2)
		numbers := strings.Split(parts[0], ".")
		if len(numbers) != 3 {
			return core, "", false
		}
		for index := range core {
			number, err := strconv.Atoi(numbers[index])
			if err != nil || number < 0 {
				return core, "", false
			}
			core[index] = number
		}
		prerelease := ""
		if len(parts) == 2 {
			prerelease = parts[1]
		}
		return core, prerelease, true
	}
	left, leftPre, leftOK := parse(current)
	right, rightPre, rightOK := parse(latest)
	if !leftOK || !rightOK {
		return versionLess(current, latest)
	}
	for index := range left {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	if leftPre == rightPre {
		return false
	}
	if leftPre != "" && rightPre == "" {
		return true
	}
	if leftPre == "" {
		return false
	}
	return versionLess(leftPre, rightPre)
}

type releaseManifestEntry struct {
	Hash string `json:"Hash"`
	File string `json:"File"`
}

func readReleaseManifest(filename string) (map[string]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var entries []releaseManifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode release checksum manifest: %w", err)
	}
	result := make(map[string]string)
	for _, entry := range entries {
		hash := strings.ToLower(strings.TrimSpace(entry.Hash))
		if filepath.Base(entry.File) != entry.File || len(hash) != 64 || !isHex(hash) {
			return nil, errors.New("release checksum manifest contains an invalid entry")
		}
		result[entry.File] = hash
	}
	return result, nil
}

func downloadReleaseAsset(ctx context.Context, client *http.Client, sourceURL, destination string, limit int64, expectedHash string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "MinerDash/"+buildinfo.Version)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned %s", filepath.Base(destination), response.Status)
	}
	temp := destination + ".download"
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	if written == 0 || written > limit {
		_ = os.Remove(temp)
		return errors.New("release asset is empty or exceeds its size limit")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if expectedHash != "" && actual != strings.ToLower(expectedHash) {
		_ = os.Remove(temp)
		return fmt.Errorf("%s SHA-256 mismatch", filepath.Base(destination))
	}
	if err := os.Rename(temp, destination); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
