package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
		"wildrig-multi":  "wildrig-multi-linux-0.51.2.tar.gz",
		"srbminer-multi": "SRBMiner-Multi-3-6-9-Linux.tar.gz",
		"lolminer":       "lolMiner_v1.98a_Lin64.tar.gz",
		"bzminer":        "bzminer_v100.31_linux.tar.gz",
		"onezerominer":   "onezerominer-linux-1.7.6.tar.gz",
	}
	for catalogID, filename := range tests {
		source, ok := catalogReleaseSources[catalogID]
		if !ok || source.LinuxArchive == nil || !source.LinuxArchive.MatchString(filename) {
			t.Errorf("%s does not match official Linux asset %q", catalogID, filename)
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
