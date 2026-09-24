package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ulikunitz/xz"
	"minerdash/internal/buildinfo"
)

const (
	rigOSBuildManifestFilename = "minerdash-rig-os.raw.build-manifest"
	maxRigOSDownloadSize       = int64(4 << 30)
)

type usbDevice struct {
	ID             string `json:"id"`
	DiskNumber     int    `json:"disk_number"`
	Name           string `json:"name"`
	SerialNumber   string `json:"serial_number,omitempty"`
	SizeBytes      int64  `json:"size_bytes"`
	PartitionStyle string `json:"partition_style,omitempty"`
	uniqueID       string
}

type usbOperation struct {
	ID             string `json:"id,omitempty"`
	Action         string `json:"action,omitempty"`
	State          string `json:"state"`
	Message        string `json:"message,omitempty"`
	DeviceID       string `json:"device_id,omitempty"`
	BytesCompleted int64  `json:"bytes_completed,omitempty"`
	BytesTotal     int64  `json:"bytes_total,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
}

type usbStatus struct {
	Supported      bool         `json:"supported"`
	ControllerURLs []string     `json:"controller_urls,omitempty"`
	TLSFingerprint string       `json:"tls_fingerprint,omitempty"`
	Devices        []usbDevice  `json:"devices"`
	Operation      usbOperation `json:"operation"`
	Error          string       `json:"error,omitempty"`
}

type usbManager struct {
	mu         sync.RWMutex
	imageDir   string
	releases   *applicationUpdateMonitor
	store      *Store
	operation  usbOperation
	httpClient *http.Client
}

func newUSBManager(dataDirectory string, releases *applicationUpdateMonitor, store *Store) *usbManager {
	return &usbManager{
		imageDir: filepath.Join(dataDirectory, "rig-os"),
		releases: releases,
		store:    store,
		httpClient: &http.Client{
			Timeout: 0,
		},
		operation: usbOperation{State: "idle"},
	}
}

func (manager *usbManager) status() usbStatus {
	manager.mu.RLock()
	operation := manager.operation
	manager.mu.RUnlock()
	devices, err := platformUSBDevices()
	status := usbStatus{
		Supported: platformUSBSupported(),
		Devices:   devices,
		Operation: operation,
	}
	if err != nil {
		status.Error = err.Error()
	}
	return status
}

func (manager *usbManager) begin(action, deviceID string, work func(string) error) (usbOperation, error) {
	manager.mu.Lock()
	if manager.operation.State == "running" {
		manager.mu.Unlock()
		return usbOperation{}, errors.New("another USB operation is already running")
	}
	id, err := randomToken(12)
	if err != nil {
		manager.mu.Unlock()
		return usbOperation{}, err
	}
	manager.operation = usbOperation{
		ID:        id,
		Action:    action,
		State:     "running",
		Message:   "Starting",
		DeviceID:  deviceID,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	operation := manager.operation
	manager.mu.Unlock()
	go func() {
		err := work(id)
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if manager.operation.ID != id {
			return
		}
		manager.operation.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			manager.operation.State = "failed"
			manager.operation.Message = err.Error()
			return
		}
		manager.operation.State = "complete"
		if action == "restore" {
			manager.operation.Message = "USB restored as a normal exFAT drive"
		} else if action == "flash" {
			manager.operation.Message = "Rig OS written, verified, and paired; safely eject the USB drive"
		} else if action == "pair" {
			manager.operation.Message = "Rig OS USB paired; safely eject it and boot the rig"
		} else {
			manager.operation.Message = "Rig OS image downloaded and verified"
		}
	}()
	return operation, nil
}

func (manager *usbManager) update(id, message string, completed, total int64) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.operation.ID != id {
		return
	}
	manager.operation.Message = message
	manager.operation.BytesCompleted = completed
	manager.operation.BytesTotal = total
}

func (manager *usbManager) ensureImage(ctx context.Context, operationID string) (string, string, int64, error) {
	imagePath := filepath.Join(manager.imageDir, rigOSImageFilename)
	status := rigOSImageStatus(manager.imageDir)
	if status.ImageAvailable {
		expectedImageHash, hashErr := readSingleHash(imagePath + ".sha256")
		actualImageHash, imageErr := packageFileSHA256(imagePath)
		rawHash, rawSize, manifestErr := readRigOSBuildManifest(filepath.Join(manager.imageDir, rigOSBuildManifestFilename))
		if hashErr == nil && imageErr == nil && manifestErr == nil && actualImageHash == expectedImageHash {
			return imagePath, rawHash, rawSize, nil
		}
	}
	manager.update(operationID, "Finding the latest Rig OS release", 0, 0)
	release, err := manager.releases.latest(ctx, true)
	if err != nil {
		return "", "", 0, err
	}
	assets := make(map[string]string)
	for _, asset := range release.Assets {
		assets[asset.Name] = asset.URL
	}
	required := []string{"SHA256SUMS.json", rigOSImageFilename, rigOSBuildManifestFilename}
	for _, name := range required {
		if assets[name] == "" {
			return "", "", 0, fmt.Errorf("release %s is missing %s", release.TagName, name)
		}
	}
	if err := os.MkdirAll(manager.imageDir, 0o700); err != nil {
		return "", "", 0, err
	}
	manifestPath := filepath.Join(manager.imageDir, "SHA256SUMS.json")
	if err := downloadReleaseAsset(ctx, manager.httpClient, assets["SHA256SUMS.json"], manifestPath, 2<<20, ""); err != nil {
		return "", "", 0, err
	}
	manifest, err := readReleaseManifest(manifestPath)
	if err != nil {
		return "", "", 0, err
	}
	buildManifestHash := manifest[rigOSBuildManifestFilename]
	imageHash := manifest[rigOSImageFilename]
	if buildManifestHash == "" || imageHash == "" {
		return "", "", 0, errors.New("release checksum manifest is missing Rig OS entries")
	}
	buildManifestPath := filepath.Join(manager.imageDir, rigOSBuildManifestFilename)
	if err := downloadReleaseAsset(ctx, manager.httpClient, assets[rigOSBuildManifestFilename], buildManifestPath, 1<<20, buildManifestHash); err != nil {
		return "", "", 0, err
	}
	rawHash, rawSize, err := readRigOSBuildManifest(buildManifestPath)
	if err != nil {
		return "", "", 0, err
	}
	manager.update(operationID, "Downloading the verified Rig OS image", 0, 0)
	if err := manager.downloadImage(ctx, operationID, assets[rigOSImageFilename], imagePath, imageHash); err != nil {
		return "", "", 0, err
	}
	if err := os.WriteFile(imagePath+".sha256", []byte(imageHash+"  "+rigOSImageFilename+"\n"), 0o600); err != nil {
		return "", "", 0, err
	}
	return imagePath, rawHash, rawSize, nil
}

func (manager *usbManager) downloadImage(ctx context.Context, operationID, sourceURL, destination, expectedHash string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "MinerDash/"+buildinfo.Version)
	response, err := manager.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned %s", filepath.Base(destination), response.Status)
	}
	if response.ContentLength > maxRigOSDownloadSize {
		return errors.New("Rig OS image exceeds the allowed download size")
	}
	temp := destination + ".download"
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	buffer := make([]byte, 4<<20)
	var written int64
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			written += int64(count)
			if written > maxRigOSDownloadSize {
				_ = file.Close()
				_ = os.Remove(temp)
				return errors.New("Rig OS image exceeds the allowed download size")
			}
			if _, err := file.Write(buffer[:count]); err != nil {
				_ = file.Close()
				_ = os.Remove(temp)
				return err
			}
			_, _ = hash.Write(buffer[:count])
			manager.update(operationID, "Downloading the verified Rig OS image", written, response.ContentLength)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			_ = os.Remove(temp)
			return readErr
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if written == 0 || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(expectedHash) {
		_ = os.Remove(temp)
		return errors.New("Rig OS image SHA-256 mismatch")
	}
	if err := os.Rename(temp, destination); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func (manager *usbManager) flash(operationID, deviceID, confirmation, controllerURL, rigName string) error {
	device, err := verifiedUSBDevice(deviceID)
	if err != nil {
		return err
	}
	if confirmation != usbEraseConfirmation(device) {
		return errors.New("erase confirmation does not match the selected USB drive")
	}
	provisioning, token, err := manager.createProvisioning(controllerURL, rigName)
	if err != nil {
		return err
	}
	paired := false
	defer func() {
		if !paired {
			_ = manager.store.RevokeEnrollmentGrant(token)
		}
	}()
	imagePath, expectedRawHash, rawSize, err := manager.ensureImage(context.Background(), operationID)
	if err != nil {
		return err
	}
	if rawSize <= 0 || device.SizeBytes < rawSize {
		return fmt.Errorf("selected USB drive is too small; at least %s is required", formatByteCount(rawSize))
	}
	device, err = verifiedUSBDevice(deviceID)
	if err != nil {
		return err
	}
	manager.update(operationID, "Erasing existing USB partitions", 0, rawSize)
	if err := platformPrepareUSB(device); err != nil {
		return err
	}
	image, err := os.Open(imagePath)
	if err != nil {
		return err
	}
	defer image.Close()
	reader, err := xz.NewReader(image)
	if err != nil {
		return fmt.Errorf("open compressed Rig OS image: %w", err)
	}
	target, err := platformOpenUSB(device, true)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := copyWithUSBProgress(io.MultiWriter(target, hash), reader, rawSize, func(done int64) {
		manager.update(operationID, "Writing Rig OS to USB", done, rawSize)
	})
	syncErr := target.Sync()
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != rawSize {
		return fmt.Errorf("Rig OS write size mismatch: wrote %d of %d bytes", written, rawSize)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedRawHash {
		return errors.New("decompressed Rig OS SHA-256 mismatch")
	}
	manager.update(operationID, "Verifying the USB contents", 0, rawSize)
	if err := verifyUSBContents(manager, operationID, device, expectedRawHash, rawSize); err != nil {
		return err
	}
	if err := platformRefreshUSB(device); err != nil {
		return err
	}
	manager.update(operationID, "Pairing USB with this controller", 0, 0)
	if err := platformWriteUSBProvisioning(device, provisioning); err != nil {
		return err
	}
	paired = true
	return nil
}

func (manager *usbManager) restore(operationID, deviceID, confirmation string) error {
	device, err := verifiedUSBDevice(deviceID)
	if err != nil {
		return err
	}
	if confirmation != usbEraseConfirmation(device) {
		return errors.New("erase confirmation does not match the selected USB drive")
	}
	manager.update(operationID, "Removing Miner Dash partitions and formatting exFAT", 0, device.SizeBytes)
	return platformRestoreUSB(device)
}

func (manager *usbManager) pair(operationID, deviceID, controllerURL, rigName string) error {
	device, err := verifiedUSBDevice(deviceID)
	if err != nil {
		return err
	}
	provisioning, token, err := manager.createProvisioning(controllerURL, rigName)
	if err != nil {
		return err
	}
	if err := platformWriteUSBProvisioning(device, provisioning); err != nil {
		_ = manager.store.RevokeEnrollmentGrant(token)
		return err
	}
	return nil
}

func (manager *usbManager) createProvisioning(controllerURL, rigName string) ([]byte, string, error) {
	if err := validateProvisioning(controllerURL, rigName, manager.store.TLSFingerprint()); err != nil {
		return nil, "", err
	}
	token, err := manager.store.CreateEnrollmentGrant(rigName, 30*24*time.Hour)
	if err != nil {
		return nil, "", err
	}
	provisioning := fmt.Sprintf(
		"version=1\ncontroller=%s\nfingerprint=%s\nname=%s\ntoken=%s\n",
		controllerURL,
		strings.ToLower(manager.store.TLSFingerprint()),
		rigName,
		token,
	)
	return []byte(provisioning), token, nil
}

var provisionedRigNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

func validateProvisioning(controllerURL, rigName, fingerprint string) error {
	if !strings.HasPrefix(controllerURL, "https://") || strings.ContainsAny(controllerURL, "\r\n\t ") {
		return errors.New("select a valid HTTPS controller address")
	}
	hostPort := strings.TrimPrefix(controllerURL, "https://")
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil || host == "" {
		return errors.New("controller address must include an HTTPS host and port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("controller port is invalid")
	}
	if !provisionedRigNamePattern.MatchString(rigName) {
		return errors.New("rig name must start with a letter or number and contain only letters, numbers, dots, underscores, or hyphens")
	}
	fingerprint = strings.ToLower(strings.ReplaceAll(fingerprint, ":", ""))
	if len(fingerprint) != 64 || !isHex(fingerprint) {
		return errors.New("controller TLS fingerprint is unavailable")
	}
	return nil
}

func verifiedUSBDevice(id string) (usbDevice, error) {
	devices, err := platformUSBDevices()
	if err != nil {
		return usbDevice{}, err
	}
	for _, device := range devices {
		if device.ID == id {
			return device, nil
		}
	}
	return usbDevice{}, errors.New("the selected USB drive is no longer connected or no longer matches")
}

func usbEraseConfirmation(device usbDevice) string {
	return "ERASE USB " + strconv.Itoa(device.DiskNumber)
}

func fingerprintUSBDevice(device usbDevice) string {
	value := fmt.Sprintf("%d|%s|%s|%d", device.DiskNumber, device.uniqueID, device.SerialNumber, device.SizeBytes)
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func verifyUSBContents(manager *usbManager, operationID string, device usbDevice, expectedHash string, size int64) error {
	target, err := platformOpenUSB(device, false)
	if err != nil {
		return err
	}
	defer target.Close()
	hash := sha256.New()
	read, err := copyWithUSBProgress(hash, io.LimitReader(target, size), size, func(done int64) {
		manager.update(operationID, "Verifying the USB contents", done, size)
	})
	if err != nil {
		return err
	}
	if read != size || hex.EncodeToString(hash.Sum(nil)) != expectedHash {
		return errors.New("USB verification failed; do not boot from this drive")
	}
	return nil
}

func copyWithUSBProgress(destination io.Writer, source io.Reader, total int64, progress func(int64)) (int64, error) {
	buffer := make([]byte, 4<<20)
	var written int64
	for {
		count, readErr := source.Read(buffer)
		if count > 0 {
			outputCount, writeErr := destination.Write(buffer[:count])
			written += int64(outputCount)
			progress(written)
			if writeErr != nil {
				return written, writeErr
			}
			if outputCount != count {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func readSingleHash(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", errors.New("invalid SHA-256 file")
	}
	hash := strings.ToLower(fields[0])
	if len(hash) != 64 || !isHex(hash) {
		return "", errors.New("invalid SHA-256 file")
	}
	return hash, nil
}

func readRigOSBuildManifest(filename string) (string, int64, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", 0, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = value
		}
	}
	hash := strings.ToLower(values["output_sha256"])
	if len(hash) != 64 || !isHex(hash) {
		return "", 0, errors.New("Rig OS build manifest has an invalid raw image SHA-256")
	}
	size, err := strconv.ParseInt(values["size_bytes"], 10, 64)
	if err != nil || size <= 0 {
		return "", 0, errors.New("Rig OS build manifest has an invalid image size")
	}
	return hash, size, nil
}

func rigOSImageStatus(imageDirectory string) struct {
	ImageAvailable bool
	ImageSizeBytes int64
} {
	path := filepath.Join(imageDirectory, rigOSImageFilename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return struct {
			ImageAvailable bool
			ImageSizeBytes int64
		}{}
	}
	if _, err := readSingleHash(path + ".sha256"); err != nil {
		return struct {
			ImageAvailable bool
			ImageSizeBytes int64
		}{}
	}
	return struct {
		ImageAvailable bool
		ImageSizeBytes int64
	}{true, info.Size()}
}

func formatByteCount(size int64) string {
	return fmt.Sprintf("%.1f GiB", float64(size)/(1<<30))
}
