package server

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"minerdash/internal/protocol"
)

const maxMinerBinarySize = 250 << 20
const maxMinerPackageSize = 1 << 30

func packageFileSHA256(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Store) SaveMinerPackage(minerID string, source io.Reader) (protocol.MinerDefinition, error) {
	s.mu.RLock()
	miner, ok := s.state.Miners[minerID]
	s.mu.RUnlock()
	if !ok {
		return protocol.MinerDefinition{}, ErrNotFound
	}
	if miner.PackageFormat == "tar.gz" {
		return s.SaveMinerBundle(minerID, source)
	}
	return s.SaveMinerBinary(minerID, source)
}

func (s *Store) SaveMinerBinary(minerID string, source io.Reader) (protocol.MinerDefinition, error) {
	return s.saveMinerBinary(minerID, source, "")
}

func (s *Store) saveMinerBinary(minerID string, source io.Reader, expectedHash string) (protocol.MinerDefinition, error) {
	s.mu.RLock()
	miner, ok := s.state.Miners[minerID]
	s.mu.RUnlock()
	if !ok {
		return protocol.MinerDefinition{}, ErrNotFound
	}
	if miner.BinaryName == "" || filepath.Base(miner.BinaryName) != miner.BinaryName {
		return protocol.MinerDefinition{}, errors.New("miner binary_name must be a file name without directories")
	}
	if err := os.MkdirAll(s.packageDir, 0o700); err != nil {
		return protocol.MinerDefinition{}, err
	}
	temp, err := os.CreateTemp(s.packageDir, minerID+"-*.upload")
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(source, maxMinerBinarySize+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return protocol.MinerDefinition{}, copyErr
	}
	if closeErr != nil {
		return protocol.MinerDefinition{}, closeErr
	}
	if written > maxMinerBinarySize {
		return protocol.MinerDefinition{}, errors.New("miner binary exceeds 250 MiB")
	}
	if written == 0 {
		return protocol.MinerDefinition{}, errors.New("miner binary is empty")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if expectedHash != "" && !strings.EqualFold(digest, expectedHash) {
		return protocol.MinerDefinition{}, errors.New("custom miner executable SHA-256 does not match the approved value")
	}
	finalPath := filepath.Join(s.packageDir, minerID+".bin")
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.state.Miners[minerID]
	if !ok {
		return protocol.MinerDefinition{}, ErrNotFound
	}
	if !reflect.DeepEqual(current, miner) {
		return protocol.MinerDefinition{}, errors.New("miner definition changed during upload; retry the upload")
	}
	backupPath, hadPrevious, err := stagePackageFile(tempPath, finalPath)
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	miner = current
	miner.BinarySHA256 = digest
	miner.PackageSHA256 = ""
	miner.PackageFormat = "binary"
	miner.ManagedBinary = true
	s.state.Miners[minerID] = miner
	s.bumpWorkersForResourceLocked("miner", minerID)
	s.recordLocked("upload", "miner-binary", minerID, map[string]any{"sha256": digest, "bytes": written})
	if err := s.saveLocked(); err != nil {
		_ = os.Remove(finalPath)
		if hadPrevious {
			_ = os.Rename(backupPath, finalPath)
		}
		return protocol.MinerDefinition{}, err
	}
	if hadPrevious {
		_ = os.Remove(backupPath)
	}
	_ = os.Remove(filepath.Join(s.packageDir, minerID+".tar.gz"))
	return miner, nil
}

func (s *Store) SaveMinerBundle(minerID string, source io.Reader) (protocol.MinerDefinition, error) {
	return s.saveMinerBundle(minerID, source, "", "")
}

func (s *Store) saveMinerBundle(minerID string, source io.Reader, expectedPackageHash, expectedEntryHash string) (protocol.MinerDefinition, error) {
	s.mu.RLock()
	miner, ok := s.state.Miners[minerID]
	s.mu.RUnlock()
	if !ok {
		return protocol.MinerDefinition{}, ErrNotFound
	}
	if miner.PackageFormat != "tar.gz" || !validBundlePath(miner.EntryPoint) {
		return protocol.MinerDefinition{}, errors.New("miner bundle metadata is incomplete")
	}
	if err := os.MkdirAll(s.packageDir, 0o700); err != nil {
		return protocol.MinerDefinition{}, err
	}
	temp, err := os.CreateTemp(s.packageDir, minerID+"-*.bundle")
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(source, maxMinerPackageSize+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return protocol.MinerDefinition{}, copyErr
	}
	if closeErr != nil {
		return protocol.MinerDefinition{}, closeErr
	}
	if written == 0 || written > maxMinerPackageSize {
		return protocol.MinerDefinition{}, errors.New("miner bundle is empty or exceeds 1 GiB")
	}
	entryHash, err := validateMinerBundle(tempPath, miner.EntryPoint)
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	packageHash := hex.EncodeToString(hash.Sum(nil))
	if expectedPackageHash != "" && !strings.EqualFold(packageHash, expectedPackageHash) {
		return protocol.MinerDefinition{}, errors.New("custom miner package SHA-256 does not match the approved value")
	}
	if expectedEntryHash != "" && !strings.EqualFold(entryHash, expectedEntryHash) {
		return protocol.MinerDefinition{}, errors.New("custom miner executable SHA-256 does not match the approved value")
	}
	finalPath := filepath.Join(s.packageDir, minerID+".tar.gz")
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.state.Miners[minerID]
	if !ok {
		return protocol.MinerDefinition{}, ErrNotFound
	}
	if !reflect.DeepEqual(current, miner) {
		return protocol.MinerDefinition{}, errors.New("miner definition changed during upload; retry the upload")
	}
	backupPath, hadPrevious, err := stagePackageFile(tempPath, finalPath)
	if err != nil {
		return protocol.MinerDefinition{}, err
	}
	current.BinarySHA256 = entryHash
	current.PackageSHA256 = packageHash
	current.ManagedBinary = true
	s.state.Miners[minerID] = current
	s.bumpWorkersForResourceLocked("miner", minerID)
	s.recordLocked("upload", "miner-bundle", minerID, map[string]any{"sha256": current.PackageSHA256, "entry_sha256": entryHash, "bytes": written})
	if err := s.saveLocked(); err != nil {
		_ = os.Remove(finalPath)
		if hadPrevious {
			_ = os.Rename(backupPath, finalPath)
		}
		return protocol.MinerDefinition{}, err
	}
	if hadPrevious {
		_ = os.Remove(backupPath)
	}
	_ = os.Remove(filepath.Join(s.packageDir, minerID+".bin"))
	return current, nil
}

func validateMinerBundle(filename, entryPoint string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("open miner bundle: %w", err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	var total int64
	var files int
	var entryDigest string
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read miner bundle: %w", err)
		}
		archiveName := header.Name
		if header.Typeflag == tar.TypeDir {
			archiveName = strings.TrimSuffix(archiveName, "/")
		}
		clean := path.Clean(strings.ReplaceAll(archiveName, "\\", "/"))
		if !validBundlePath(clean) || clean != archiveName {
			return "", fmt.Errorf("miner bundle contains unsafe path %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir {
			return "", fmt.Errorf("miner bundle contains unsupported entry %q", header.Name)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		files++
		total += header.Size
		if files > 10000 || header.Size < 0 || header.Size > maxMinerBinarySize || total > maxMinerPackageSize {
			return "", errors.New("miner bundle expands beyond safety limits")
		}
		if clean == entryPoint {
			sum := sha256.New()
			if _, err := io.Copy(sum, archive); err != nil {
				return "", err
			}
			entryDigest = hex.EncodeToString(sum.Sum(nil))
		}
	}
	if entryDigest == "" {
		return "", fmt.Errorf("miner bundle does not contain entry point %q", entryPoint)
	}
	return entryDigest, nil
}

func validBundlePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func stagePackageFile(staged, destination string) (string, bool, error) {
	backup := destination + ".previous"
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	hadPrevious := false
	if _, err := os.Stat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			return "", false, err
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if err := os.Rename(staged, destination); err != nil {
		if hadPrevious {
			_ = os.Rename(backup, destination)
		}
		return "", false, err
	}
	return backup, hadPrevious, nil
}

func (s *Store) MinerBinaryForRig(rigID, minerID string) (string, protocol.MinerDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rig, ok := s.state.Rigs[rigID]
	if !ok {
		return "", protocol.MinerDefinition{}, ErrNotFound
	}
	sheet, ok := s.state.FlightSheets[rig.Desired.FlightSheetID]
	if !ok || sheet.MinerID != minerID {
		return "", protocol.MinerDefinition{}, ErrUnauthorized
	}
	miner, ok := s.state.Miners[minerID]
	if !ok || !miner.ManagedBinary || strings.TrimSpace(miner.BinarySHA256) == "" {
		return "", protocol.MinerDefinition{}, ErrNotFound
	}
	suffix := ".bin"
	if miner.PackageFormat == "tar.gz" {
		suffix = ".tar.gz"
		if strings.TrimSpace(miner.PackageSHA256) == "" {
			return "", protocol.MinerDefinition{}, ErrNotFound
		}
	}
	filename := filepath.Join(s.packageDir, minerID+suffix)
	if _, err := os.Stat(filename); err != nil {
		return "", protocol.MinerDefinition{}, fmt.Errorf("miner package unavailable: %w", err)
	}
	return filename, miner, nil
}
