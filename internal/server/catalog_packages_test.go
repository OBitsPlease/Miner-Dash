package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDownloadTarGzipExecutableSelectsOnlyExpectedBinary(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	archive := tar.NewWriter(gzipWriter)
	files := map[string]string{
		"SRBMiner-Multi/readme.txt":     "documentation",
		"SRBMiner-Multi/SRBMiner-MULTI": "verified executable",
	}

	for name, value := range files {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(value)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "SRBMiner-MULTI")
	if err := downloadTarGzipExecutable(context.Background(), server.Client(), server.URL, "SRBMiner-MULTI", destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "verified executable" {
		t.Fatalf("downloaded executable = %q", data)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("downloaded executable permissions = %o", info.Mode().Perm())
	}
}

func TestDownloadLatestCatalogExecutableAcceptsReleaseWithoutPublishedDigest(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	archive := tar.NewWriter(gzipWriter)
	payload := []byte("verified t-rex executable")
	if err := archive.WriteHeader(&tar.Header{Name: "t-rex-0.26.8/t-rex", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/releases/latest") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"tag_name":"0.26.8","assets":[{"name":"t-rex-0.26.8-linux.tar.gz","browser_download_url":"https://github.com/trexminer/T-Rex/releases/download/0.26.8/t-rex-0.26.8-linux.tar.gz"}]}`)
			return
		}
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	baseTransport := server.Client().Transport
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clonedURL := *request.URL
		clonedURL.Scheme = target.Scheme
		clonedURL.Host = target.Host
		clone.URL = &clonedURL
		clone.Host = target.Host
		return baseTransport.RoundTrip(clone)
	})}
	destination := filepath.Join(t.TempDir(), "t-rex")
	version, err := downloadLatestCatalogExecutable(t.Context(), client, catalogReleaseSources["trex"], "t-rex", destination)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.26.8" || !bytes.Equal(downloaded, payload) {
		t.Fatalf("version = %q, executable = %q", version, downloaded)
	}
}

func TestOfficialReleaseHostsAreRestricted(t *testing.T) {
	for _, host := range []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"} {
		if !allowedReleaseHost(host) {
			t.Fatalf("official host %q was rejected", host)
		}
	}
	for _, host := range []string{"github.com.example.org", "example.org", ""} {
		if allowedReleaseHost(host) {
			t.Fatalf("unapproved host %q was accepted", host)
		}
	}
}

func TestAutomaticCatalogLinuxArchivePatterns(t *testing.T) {
	tests := map[string]string{
		"miniz":          "miniZ_v2.5e3_linux-x64.tar.gz",
		"veil-miner-cpu": "xmrig-v6.25.0-veil-rc2-linux-x64.tar.gz",
		"wildrig-multi":  "wildrig-multi-linux-0.51.2.tar.gz",
		"srbminer-multi": "SRBMiner-Multi-3-6-9-Linux.tar.gz",
		"lolminer":       "lolMiner_v1.98a_Lin64.tar.gz",
		"bzminer":        "bzminer_v100.31_linux.tar.gz",
		"onezerominer":   "onezerominer-linux-1.7.6.tar.gz",
		"trex":           "t-rex-0.26.8-linux.tar.gz",
		"teamredminer":   "teamredminer-v0.10.21-linux.tgz",
		"gminer":         "gminer_3_44_linux64.tar.xz",
		"ethminer":       "ethminer-0.18.0-cuda-9-linux-x86_64.tar.gz",
	}

	for catalogID, filename := range tests {
		source, ok := catalogReleaseSources[catalogID]
		if !ok || source.LinuxArchive == nil || !source.LinuxArchive.MatchString(filename) {
			t.Errorf("%s does not match official Linux asset %q", catalogID, filename)
		}
	}
}

func TestAutomaticCatalogMinersHaveVerifiedReleaseSources(t *testing.T) {
	for _, entry := range minerCatalog {
		if !entry.AutomaticInstall || entry.ImageIncluded || entry.ID == "epic-miner" {
			continue
		}
		source, ok := catalogReleaseSources[entry.ID]
		if !ok || source.LinuxArchive == nil {
			t.Errorf("automatic catalog miner %q has no Linux release asset rule", entry.ID)
		}
	}
}

func TestEpicCatalogSeedsPinnedLocalPackage(t *testing.T) {
	entry, ok := catalogEntry("epic-miner")
	if !ok {
		t.Fatal("Epic miner catalog entry is missing")
	}
	if entry.PackageSHA256 != "8abb5712a985fca571cbe0780b8f7554dbac6ac1d1f5eb0c9bcdcd75e50ce2e0" ||
		entry.SourceCommit != "9e33237186e380480a982f57fbab6c1ab3743724" ||
		entry.EntryPoint != "epic-miner-launch" ||
		entry.Environment["XDG_STATE_HOME"] != "/var/lib/minerdash" {
		t.Fatalf("Epic catalog metadata = %#v", entry)
	}
	for _, argument := range entry.DefaultArguments {
		if argument == "--threads" || strings.HasPrefix(argument, "--threads=") {
			t.Fatalf("Epic catalog defaults must omit --threads so the launcher uses all logical cores: %q", entry.DefaultArguments)
		}
	}
	if got := strings.Join(entry.DefaultArguments, " "); got != "--stratum {POOL} --stratum-login {STRATUM_LOGIN}" {
		t.Fatalf("Epic catalog arguments = %q", got)
	}
	seed := catalogPackageSeeds["epic-miner"]
	sum := sha256.Sum256(seed.archive)
	if hex.EncodeToString(sum[:]) != entry.PackageSHA256 {
		t.Fatal("embedded Epic package does not match its pinned SHA-256")
	}

	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.EnsureCatalogMinerPackage(t.Context(), "epic-miner", false)
	if err != nil {
		t.Fatal(err)
	}
	if miner.Version != "2.3.3" || miner.Algorithm != "randomx" || miner.PackageSHA256 != entry.PackageSHA256 ||
		miner.BinarySHA256 == "" || miner.StatsType != "" ||
		miner.Environment["XDG_STATE_HOME"] != "/var/lib/minerdash" {
		t.Fatalf("seeded Epic miner = %#v", miner)
	}
	installed, err := os.ReadFile(filepath.Join(store.packageDir, miner.ID+".tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	installedSum := sha256.Sum256(installed)
	if hex.EncodeToString(installedSum[:]) != entry.PackageSHA256 {
		t.Fatal("installed Epic package does not match its pinned SHA-256")
	}
}

func TestInstallEpicCatalogMinerRefreshesRequiredEnvironment(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.InstallCatalogMiner("epic-miner")
	if err != nil {
		t.Fatal(err)
	}
	miner.Environment = nil
	if _, err := store.SaveMiner(miner); err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.InstallCatalogMiner("epic-miner")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Environment["XDG_STATE_HOME"] != "/var/lib/minerdash" {
		t.Fatalf("Epic environment was not refreshed: %#v", refreshed.Environment)
	}
}

func TestRefreshInstalledEmbeddedMinerPackagesUpdatesEpicPackage(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.InstallCatalogMiner("epic-miner")
	if err != nil {
		t.Fatal(err)
	}
	miner.PackageSHA256 = strings.Repeat("0", 64)
	miner.BinarySHA256 = strings.Repeat("1", 64)
	if _, err := store.SaveMiner(miner); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshInstalledEmbeddedMinerPackages(t.Context()); err != nil {
		t.Fatal(err)
	}
	refreshed := store.ListMiners()[0]
	if refreshed.PackageSHA256 != catalogPackageSeeds["epic-miner"].packageSHA256 ||
		refreshed.BinarySHA256 != "5b80d05b31e1a614d225f031433f0b1c0bc1493fc01855d8e1d1f94091dd13e3" {
		t.Fatalf("refreshed Epic package = %#v", refreshed)
	}
}

func TestEpicEmbeddedLauncherSupportsStratumTransportsIPv6AndThreadClamp(t *testing.T) {
	launcher := string(bundleEntry(t, epicMinerPackage, "epic-miner-launch"))
	for _, required := range []string{
		"stratum+tcp://*|tcp://*",
		"stratum+ssl://*|stratum+tls://*|ssl://*|tls://*",
		"--stratum-login",
		`stratum_server_login = "$stratum_login"`,
		`|\[[0-9A-Fa-f:]+\]):[0-9]{1,5}$`,
		"port < 1 || port > 65535",
		`threads="$(nproc)"`,
		`threads="255"`,
	} {
		if !strings.Contains(launcher, required) {
			t.Fatalf("embedded Epic launcher does not contain %q", required)
		}
	}
	if strings.Contains(strings.Join(catalogEntryMust(t, "epic-miner").DefaultArguments, " "), "--threads") {
		t.Fatal("Epic launcher must retain automatic thread detection")
	}
	if !strings.Contains(launcher, `large_pages="false"`) ||
		!strings.Contains(launcher, `large_pages = $large_pages`) {
		t.Fatal("Epic launcher does not default to reliable standard-page RandomX mining")
	}
	if !bytes.Equal(bundleEntry(t, epicMinerPackage, "epic-miner-launch"), epicMinerLauncher) {
		t.Fatal("embedded EPIC launcher differs from its maintained source")
	}
}

func TestEpicEmbeddedLauncherResolvesStratumURLs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Epic launcher targets Linux")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is unavailable")
	}
	tests := []struct {
		name       string
		poolURL    string
		wantServer string
		wantTLS    string
	}{
		{name: "tcp", poolURL: "stratum+tcp://192.0.2.44:3416", wantServer: "192.0.2.44:3416", wantTLS: "false"},
		{name: "ssl", poolURL: "stratum+ssl://pool.invalid:3416", wantServer: "pool.invalid:3416", wantTLS: "true"},
		{name: "tls-ipv6", poolURL: "stratum+tls://[2001:db8::44]:3416", wantServer: "[2001:db8::44]:3416", wantTLS: "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := runEpicLauncher(t, test.poolURL, "8", "--threads", "1", "--stratum-login", "CPU-Rig-01", "--large-pages")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(config, `stratum_server_addr = "`+test.wantServer+`"`) ||
				!strings.Contains(config, `stratum_server_login = "CPU-Rig-01"`) ||
				!strings.Contains(config, "large_pages = true") ||
				!strings.Contains(config, "stratum_server_tls_enabled = "+test.wantTLS) {
				t.Fatalf("launcher config for %q:\n%s", test.poolURL, config)
			}
		})
	}
}

func TestEpicEmbeddedLauncherClampsDetectedThreadsAndValidatesPorts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Epic launcher targets Linux")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is unavailable")
	}
	config, err := runEpicLauncher(t, "stratum+tcp://pool.invalid:3416", "999")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, "threads = 255") {
		t.Fatalf("launcher did not clamp detected processor count:\n%s", config)
	}
	for _, poolURL := range []string{"stratum+tcp://pool.invalid:0", "stratum+tls://[2001:db8::44]:65536"} {
		if _, err := runEpicLauncher(t, poolURL, "8", "--threads", "1"); err == nil {
			t.Fatalf("launcher accepted invalid pool port in %q", poolURL)
		}
	}
}

func TestEpicEmbeddedPackageRecoversMissingOrCorruptFile(t *testing.T) {
	for _, test := range []struct {
		name   string
		damage func(string) error
	}{
		{name: "missing", damage: os.Remove},
		{name: "corrupt", damage: func(path string) error { return os.WriteFile(path, []byte("corrupt"), 0o600) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
			if err != nil {
				t.Fatal(err)
			}
			miner, err := store.EnsureCatalogMinerPackage(t.Context(), "epic-miner", false)
			if err != nil {
				t.Fatal(err)
			}
			packagePath := filepath.Join(store.packageDir, miner.ID+".tar.gz")
			if err := test.damage(packagePath); err != nil {
				t.Fatal(err)
			}
			recovered, err := store.EnsureCatalogMinerPackage(t.Context(), "epic-miner", false)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.PackageSHA256 != catalogPackageSeeds["epic-miner"].packageSHA256 {
				t.Fatalf("recovered package metadata = %q", recovered.PackageSHA256)
			}
			installed, err := os.ReadFile(packagePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(installed, epicMinerPackage) {
				t.Fatal("embedded Epic package was not restored")
			}
		})
	}
}

func runEpicLauncher(t *testing.T, poolURL, processorCount string, extraArgs ...string) (string, error) {
	t.Helper()
	root := t.TempDir()
	launcherPath := filepath.Join(root, "epic-miner-launch")
	if err := os.WriteFile(launcherPath, bundleEntry(t, epicMinerPackage, "epic-miner-launch"), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeMiner := []byte("#!/bin/sh\ncat \"$2\"\n")
	if err := os.WriteFile(filepath.Join(root, "epic-miner"), fakeMiner, 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "nproc"), []byte("#!/bin/sh\necho "+processorCount+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	args := append([]string{launcherPath, "--stratum", poolURL}, extraArgs...)
	command := exec.Command("bash", args...)
	command.Env = append(os.Environ(),
		"HOME="+root,
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func catalogEntryMust(t *testing.T, id string) protocol.MinerCatalogEntry {
	t.Helper()
	entry, ok := catalogEntry(id)
	if !ok {
		t.Fatalf("catalog entry %q is missing", id)
	}
	return entry
}

func bundleEntry(t *testing.T, bundle []byte, name string) []byte {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("bundle entry %q is missing", name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == name {
			content, err := io.ReadAll(archive)
			if err != nil {
				t.Fatal(err)
			}
			return content
		}
	}
}

func TestDownloadTarGzipExecutableVerifiesPublishedDigest(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	archive := tar.NewWriter(gzipWriter)
	content := []byte("verified executable")
	if err := archive.WriteHeader(&tar.Header{Name: "SRBMiner-MULTI", Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()
	sum := sha256.Sum256(compressed.Bytes())
	digest := "sha256:" + hex.EncodeToString(sum[:])
	destination := filepath.Join(t.TempDir(), "SRBMiner-MULTI")
	if err := downloadTarGzipExecutableWithDigest(context.Background(), server.Client(), server.URL, digest, "SRBMiner-MULTI", destination); err != nil {
		t.Fatal(err)
	}
	if err := downloadTarGzipExecutableWithDigest(context.Background(), server.Client(), server.URL, "sha256:"+strings.Repeat("0", 64), "SRBMiner-MULTI", destination); err == nil {
		t.Fatal("download accepted an archive with the wrong published digest")
	}
}

func TestDownloadCustomMinerAcceptsDirectExecutableAndRejectsHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/page" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>not a miner</html>"))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("custom executable"))
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "custom-miner")
	if err := downloadCustomMiner(context.Background(), server.Client(), server.URL+"/binary", "custom-miner", destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "custom executable" {
		t.Fatalf("custom executable = %q, error = %v", data, err)
	}
	if err := downloadCustomMiner(context.Background(), server.Client(), server.URL+"/page", "custom-miner", destination); err == nil {
		t.Fatal("downloadCustomMiner() accepted an HTML page")
	}
}

func TestCustomMinerURLRequiresHTTPSWithoutCredentials(t *testing.T) {
	for _, raw := range []string{"http://example.com/miner", "https://user:password@example.com/miner", "file:///tmp/miner"} {
		if _, err := validatePublicHTTPSURL(raw); err == nil {
			t.Fatalf("validatePublicHTTPSURL(%q) succeeded", raw)
		}
	}
	if _, err := validatePublicHTTPSURL("https://github.com/project/releases/miner.tar.gz"); err != nil {
		t.Fatal(err)
	}
}
