package server

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

func (s *HTTPServer) importCustomMiner(w http.ResponseWriter, r *http.Request) {
	var input protocol.CustomMinerImport
	if !decodeJSON(w, r, &input) {
		return
	}
	miner, err := s.store.ImportCustomMiner(r.Context(), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, miner)
}

func (s *Store) ImportCustomMiner(ctx context.Context, input protocol.CustomMinerImport) (result protocol.MinerDefinition, resultErr error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Version = strings.TrimSpace(input.Version)
	input.Algorithm = strings.TrimSpace(input.Algorithm)
	input.URL = strings.TrimSpace(input.URL)
	input.BinaryName = strings.TrimSpace(input.BinaryName)
	input.PackageFormat = strings.TrimSpace(input.PackageFormat)
	input.EntryPoint = strings.TrimSpace(input.EntryPoint)
	input.ExpectedBinarySHA256 = strings.ToLower(strings.TrimSpace(input.ExpectedBinarySHA256))
	input.ExpectedPackageSHA256 = strings.ToLower(strings.TrimSpace(input.ExpectedPackageSHA256))
	if input.Name == "" || input.Algorithm == "" || input.URL == "" || input.BinaryName == "" {
		return protocol.MinerDefinition{}, errors.New("custom miner name, algorithm, HTTPS download URL, and executable name are required")
	}
	if input.PackageFormat == "" {
		input.PackageFormat = "binary"
	}
	if input.PackageFormat == "tar.gz" {
		if !validBundlePath(input.EntryPoint) {
			return protocol.MinerDefinition{}, errors.New("custom miner bundle entry point must be a safe relative path")
		}
	} else if input.PackageFormat != "binary" {
		return protocol.MinerDefinition{}, errors.New("custom miner package format must be binary or tar.gz")
	}
	if filepath.Base(input.BinaryName) != input.BinaryName {
		return protocol.MinerDefinition{}, errors.New("custom miner executable name must not contain a directory")
	}
	if input.ExpectedBinarySHA256 != "" && (len(input.ExpectedBinarySHA256) != 64 || !isHex(input.ExpectedBinarySHA256)) {
		return protocol.MinerDefinition{}, errors.New("expected executable SHA-256 must be a 64-character hexadecimal digest")
	}
	if input.ExpectedPackageSHA256 != "" && (len(input.ExpectedPackageSHA256) != 64 || !isHex(input.ExpectedPackageSHA256)) {
		return protocol.MinerDefinition{}, errors.New("expected package SHA-256 must be a 64-character hexadecimal digest")
	}
	parsed, err := validatePublicHTTPSURL(input.URL)
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	if input.Version == "" {
		input.Version = "custom"
	}
	var previous *protocol.MinerDefinition
	if input.CatalogID != "" {
		s.mu.RLock()
		for _, candidate := range s.state.Miners {
			if candidate.CatalogID == input.CatalogID {
				copy := candidate
				previous = &copy
				break
			}
		}
		s.mu.RUnlock()
	}
	miner, err := s.SaveMiner(protocol.MinerDefinition{
		CatalogID: input.CatalogID, Name: input.Name, Version: input.Version, Algorithm: input.Algorithm,
		ManagedBinary: true, BinaryName: input.BinaryName, SourceURL: parsed.String(),
		PackageFormat: input.PackageFormat, EntryPoint: input.EntryPoint,
		Environment: input.Environment, StatsType: input.StatsType, StatsURL: input.StatsURL,
		DefaultArguments: append([]string(nil), input.DefaultArguments...),
	})
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			var cleanupErr error
			if previous != nil {
				_, cleanupErr = s.SaveMiner(*previous)
			} else {
				cleanupErr = s.DeleteResource("miners", miner.ID)
			}
			if cleanupErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("restore miner after failed import: %w", cleanupErr))
			}
		}
	}()
	if err := os.MkdirAll(s.packageDir, 0o700); err != nil {
		return protocol.MinerDefinition{}, err
	}
	var saved protocol.MinerDefinition
	if input.PackageFormat == "tar.gz" {
		response, err := fetchCustomMinerPackage(ctx, publicDownloadClient(), parsed.String())
		if err != nil {
			return protocol.MinerDefinition{}, fmt.Errorf("download custom miner: %w", err)
		}
		saved, err = s.saveMinerBundle(miner.ID, response.Body, input.ExpectedPackageSHA256, input.ExpectedBinarySHA256)
		closeErr := response.Body.Close()
		if err != nil {
			return protocol.MinerDefinition{}, err
		}
		if closeErr != nil {
			return protocol.MinerDefinition{}, closeErr
		}
	} else {
		temp, err := os.CreateTemp(s.packageDir, miner.ID+"-*.custom")
		if err != nil {
			return protocol.MinerDefinition{}, err
		}
		tempPath := temp.Name()
		if err := temp.Close(); err != nil {
			_ = os.Remove(tempPath)
			return protocol.MinerDefinition{}, err
		}
		defer os.Remove(tempPath)
		if err := downloadCustomMiner(ctx, publicDownloadClient(), parsed.String(), input.BinaryName, tempPath); err != nil {
			return protocol.MinerDefinition{}, fmt.Errorf("download custom miner: %w", err)
		}
		file, err := os.Open(tempPath)
		if err != nil {
			return protocol.MinerDefinition{}, err
		}
		var saveErr error
		saved, saveErr = s.saveMinerBinary(miner.ID, file, input.ExpectedBinarySHA256)
		closeErr := file.Close()
		if saveErr != nil {
			return protocol.MinerDefinition{}, saveErr
		}
		if closeErr != nil {
			return protocol.MinerDefinition{}, closeErr
		}
	}
	cleanup = false
	return saved, nil
}

func fetchCustomMinerPackage(ctx context.Context, client *http.Client, sourceURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "MinerDash/0.1")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("download returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		response.Body.Close()
		return nil, errors.New("download URL returned an HTML page instead of a miner package")
	}
	return response, nil
}

func validatePublicHTTPSURL(raw string) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, errors.New("custom miner URL is too long")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("custom miner URL must be a public HTTPS URL without embedded credentials")
	}
	return parsed, nil
}

func publicDownloadClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, address := range addresses {
				if isPublicIP(address.IP) {
					return dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
				}
			}
			return nil, errors.New("custom miner host does not resolve to a public IP address")
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   3 * time.Minute,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			if len(previous) >= 5 {
				return errors.New("custom miner download has too many redirects")
			}
			_, err := validatePublicHTTPSURL(request.URL.String())
			return err
		},
	}
}

func isPublicIP(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}

func downloadCustomMiner(ctx context.Context, client *http.Client, sourceURL, binaryName, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "MinerDash/0.1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("download returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		return errors.New("download URL returned an HTML page instead of a miner package")
	}
	reader := bufio.NewReader(io.LimitReader(response.Body, maxMinerBinarySize+1))
	header, _ := reader.Peek(2)
	isGzip := len(header) == 2 && header[0] == 0x1f && header[1] == 0x8b
	if isGzip || strings.HasSuffix(strings.ToLower(response.Request.URL.Path), ".tar.gz") || strings.HasSuffix(strings.ToLower(response.Request.URL.Path), ".tgz") {
		return extractTarGzipExecutable(reader, binaryName, destination)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, reader)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 || written > maxMinerBinarySize {
		return errors.New("custom miner executable is empty or exceeds 250 MiB")
	}
	return os.Chmod(destination, 0o700)
}

func extractTarGzipExecutable(source io.Reader, binaryName, destination string) error {
	gzipReader, err := gzip.NewReader(source)
	if err != nil {
		return fmt.Errorf("open miner archive: %w", err)
	}
	defer gzipReader.Close()
	return extractTarExecutable(gzipReader, binaryName, destination)
}
