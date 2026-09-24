package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

type catalogReleaseSource struct {
	Repository   string
	LinuxArchive *regexp.Regexp
}

var catalogReleaseSources = map[string]catalogReleaseSource{
	"rigel": {
		Repository:   "rigelminer/rigel",
		LinuxArchive: regexp.MustCompile(`(?i)^rigel-.*-linux\.tar\.gz$`),
	},
	"wildrig-multi": {
		Repository:   "andru-kun/wildrig-multi",
		LinuxArchive: regexp.MustCompile(`(?i)^wildrig-multi-linux-.*\.tar\.gz$`),
	},
	"srbminer-multi": {
		Repository:   "doktor83/SRBMiner-Multi",
		LinuxArchive: regexp.MustCompile(`(?i)^SRBMiner-Multi-.*-Linux\.tar\.gz$`),
	},
	"miniz": {
		Repository:   "miniZ-miner/miniZ",
		LinuxArchive: regexp.MustCompile(`(?i)^miniZ_v.*_linux-x64\.tar\.gz$`),
	},
	"xmrig": {
		Repository: "xmrig/xmrig",
	},
	"lolminer": {
		Repository:   "Lolliedieb/lolMiner-releases",
		LinuxArchive: regexp.MustCompile(`(?i)^lolMiner_v.*_Lin64\.tar\.gz$`),
	},
	"teamredminer": {
		Repository: "todxx/teamredminer",
	},
	"bzminer": {
		Repository:   "bzminer/bzminer",
		LinuxArchive: regexp.MustCompile(`(?i)^bzminer_v.*_linux\.tar\.gz$`),
	},
	"gminer": {
		Repository: "develsoftware/GMinerRelease",
	},
	"trex": {
		Repository: "trexminer/T-Rex",
	},
	"ethminer": {
		Repository: "ethereum-mining/ethminer",
	},
	"hellminer": {
		Repository: "vrscms/hellminer",
	},
	"onezerominer": {
		Repository:   "OneZeroMiner/onezerominer",
		LinuxArchive: regexp.MustCompile(`(?i)^onezerominer-linux-.*\.tar\.gz$`),
	},
	"qubminer": {
		Repository: "Worm/qubminer",
	},
	"cpuminer-opt": {
		Repository: "JayDDee/cpuminer-opt",
	},
}

func (s *Store) EnsureCatalogMinerPackage(ctx context.Context, catalogID string, checkLatest bool) (protocol.MinerDefinition, error) {
	entry, found := catalogEntry(catalogID)
	if !found {
		return protocol.MinerDefinition{}, ErrNotFound
	}
	miner, err := s.InstallCatalogMiner(catalogID)
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	if entry.ImageIncluded || !entry.PackageRequired {
		return miner, nil
	}
	if miner.BinarySHA256 != "" && !checkLatest {
		return miner, nil
	}
	source, automatic := catalogReleaseSources[catalogID]
	if !automatic || source.LinuxArchive == nil {
		return protocol.MinerDefinition{}, fmt.Errorf("%s does not yet support automatic installation; use Advanced configuration for a reviewed package", entry.Name)
	}
	if err := os.MkdirAll(s.packageDir, 0o700); err != nil {
		return protocol.MinerDefinition{}, err
	}
	executable, err := os.CreateTemp(s.packageDir, catalogID+"-*.release")
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	executablePath := executable.Name()
	if err := executable.Close(); err != nil {
		_ = os.Remove(executablePath)
		return protocol.MinerDefinition{}, err
	}
	defer os.Remove(executablePath)

	version, err := downloadLatestCatalogExecutable(ctx, catalogHTTPClient(), source, entry.BinaryName, executablePath)
	if err != nil {
		return protocol.MinerDefinition{}, fmt.Errorf("install %s: %w", entry.Name, err)
	}
	file, err := os.Open(executablePath)
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	saved, saveErr := s.SaveMinerBinary(miner.ID, file)
	closeErr := file.Close()
	if saveErr != nil {
		return protocol.MinerDefinition{}, saveErr
	}
	if closeErr != nil {
		return protocol.MinerDefinition{}, closeErr
	}
	saved.Version = version
	return s.SaveMiner(saved)
}

func catalogHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" || !allowedReleaseHost(request.URL.Hostname()) {
				return errors.New("official release download redirected to an unapproved host")
			}
			return nil
		},
	}
}

func allowedReleaseHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" ||
		host == "release-assets.githubusercontent.com" ||
		host == "objects.githubusercontent.com" ||
		host == "github-releases.githubusercontent.com"
}

func downloadLatestCatalogExecutable(ctx context.Context, client *http.Client, source catalogReleaseSource, binaryName, destination string) (string, error) {
	apiURL := "https://api.github.com/repos/" + source.Repository + "/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "MinerDash/0.1")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("read official release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("official release API returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&release); err != nil {
		return "", fmt.Errorf("decode official release: %w", err)
	}
	var archiveURL string
	var archiveDigest string
	for _, asset := range release.Assets {
		if source.LinuxArchive.MatchString(asset.Name) {
			archiveURL = asset.URL
			archiveDigest = asset.Digest
			break
		}
	}
	if strings.TrimSpace(release.TagName) == "" || archiveURL == "" {
		return "", errors.New("latest official release has no supported Linux archive")
	}
	if strings.TrimSpace(archiveDigest) == "" {
		return "", errors.New("latest official Linux archive does not publish a SHA-256 digest")
	}
	parsed, err := url.Parse(archiveURL)
	if err != nil || parsed.Scheme != "https" || !allowedReleaseHost(parsed.Hostname()) {
		return "", errors.New("official release API returned an unapproved download URL")
	}
	if err := downloadTarGzipExecutableWithDigest(ctx, client, archiveURL, archiveDigest, binaryName, destination); err != nil {
		return "", err
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

func downloadTarGzipExecutable(ctx context.Context, client *http.Client, archiveURL, binaryName, destination string) error {
	return downloadTarGzipExecutableWithDigest(ctx, client, archiveURL, "", binaryName, destination)
}

func downloadTarGzipExecutableWithDigest(ctx context.Context, client *http.Client, archiveURL, expectedDigest, binaryName, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "MinerDash/0.1")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download official release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("official release download returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	hash := sha256.New()
	compressed := io.TeeReader(io.LimitReader(response.Body, maxMinerBinarySize+1), hash)
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("open official release archive: %w", err)
	}
	defer gzipReader.Close()
	if err := extractTarExecutable(gzipReader, binaryName, destination); err != nil {
		return err
	}
	if expectedDigest != "" {
		if _, err := io.Copy(io.Discard, gzipReader); err != nil {
			_ = os.Remove(destination)
			return fmt.Errorf("finish official release checksum: %w", err)
		}
		expected := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expectedDigest)), "sha256:")
		actual := hex.EncodeToString(hash.Sum(nil))
		if len(expected) != 64 || !isHex(expected) || actual != expected {
			_ = os.Remove(destination)
			return errors.New("official release archive SHA-256 does not match GitHub metadata")
		}
	}
	return nil
}

func extractTarExecutable(source io.Reader, binaryName, destination string) error {
	archive := tar.NewReader(source)
	var inspected int64
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read official release archive: %w", nextErr)
		}
		if header.Size < 0 || header.Size > maxMinerBinarySize {
			return errors.New("official release archive contains an oversized file")
		}
		inspected += header.Size
		if inspected > 1<<30 {
			return errors.New("official release archive expands beyond 1 GiB")
		}
		if header.Typeflag != tar.TypeReg || !strings.EqualFold(filepath.Base(header.Name), binaryName) {
			continue
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(output, io.LimitReader(archive, maxMinerBinarySize+1))
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written == 0 || written > maxMinerBinarySize {
			return errors.New("official release executable is empty or oversized")
		}
		return os.Chmod(destination, 0o700)
	}
	return fmt.Errorf("official release archive does not contain %s", binaryName)
}
