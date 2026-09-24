package agent

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
	"slices"
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

func TestEnsureManagedMinerEnablesXMRigStatisticsAPI(t *testing.T) {
	directory := t.TempDir()
	name := "xmrig"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(directory, name)
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	client := &Client{
		identity: Identity{Token: "controller-agent-token"},
		miner:    NewMinerManager(map[string]Profile{}),
	}
	configuration := protocol.ResolvedConfiguration{Miner: protocol.MinerDefinition{
		CatalogID:        "xmrig",
		DefaultArguments: []string{"-o", "{POOL}"},
	}}
	if _, err := client.ensureManagedMiner(context.Background(), &configuration); err != nil {
		t.Fatal(err)
	}
	profile := client.miner.profiles["xmrig"]
	for _, expected := range []string{"--http-host", "127.0.0.1", "--http-port", "18080", "--http-access-token", "--http-no-restricted"} {
		if !slices.Contains(profile.Args, expected) {
			t.Fatalf("XMRig arguments %#v do not contain %q", profile.Args, expected)
		}
	}
	if profile.StatsType != "xmrig" || profile.StatsURL != "http://127.0.0.1:18080/2/summary" || profile.StatsToken == "" {
		t.Fatalf("XMRig statistics profile = %#v", profile)
	}
}

func TestEnsureManagedMinerEnablesSRBMinerStatisticsAPI(t *testing.T) {
	packageDir := t.TempDir()
	minerID := "srbminer"
	binaryName := "SRBMiner-MULTI"
	binary := []byte("test executable")
	digest := sha256.Sum256(binary)
	directory := filepath.Join(packageDir, minerID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, binaryName), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	client := &Client{
		config: Config{PackageDir: packageDir},
		miner:  NewMinerManager(map[string]Profile{}),
	}
	configuration := protocol.ResolvedConfiguration{Miner: protocol.MinerDefinition{
		ID: minerID, ManagedBinary: true, BinaryName: binaryName,
		BinarySHA256:     hex.EncodeToString(digest[:]),
		DefaultArguments: []string{"--api-enable", "--api-port", "21550"},
	}}
	if _, err := client.ensureManagedMiner(context.Background(), &configuration); err != nil {
		t.Fatal(err)
	}
	profile := client.miner.profiles["managed-"+minerID]
	if profile.StatsType != "srbminer" || profile.StatsURL != "http://127.0.0.1:21550/" {
		t.Fatalf("SRBMiner statistics profile = %#v", profile)
	}
}

func TestManagedMinerBinaryCanRollbackAfterFailedStart(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "miner")
	staged := filepath.Join(directory, "miner.download")
	if err := os.WriteFile(destination, []byte("old version"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new version"), 0o700); err != nil {
		t.Fatal(err)
	}
	update, err := replaceManagedMinerBinary(staged, destination)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(destination); string(data) != "new version" {
		t.Fatalf("staged binary = %q", data)
	}
	if err := update.rollback(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(destination); string(data) != "old version" {
		t.Fatalf("restored binary = %q", data)
	}
}

func TestManagedMinerBundleExtractionAndDirectoryRollback(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "miner.tar.gz")
	writeAgentBundle(t, archivePath, []agentBundleEntry{
		{name: "alpha/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "alpha/alpha", body: []byte("launcher"), mode: 0o755},
		{name: "alpha/lib/libhelper.so", body: []byte("library"), mode: 0o644},
	})
	staged := filepath.Join(root, "miner.next")
	if err := extractManagedMinerBundle(archivePath, staged); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(staged, "alpha", "lib", "libhelper.so")); err != nil || string(data) != "library" {
		t.Fatalf("extracted library = %q, error = %v", data, err)
	}
	destination := filepath.Join(root, "miner")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "old"), []byte("old version"), 0o600); err != nil {
		t.Fatal(err)
	}
	update, err := replaceManagedMinerPath(staged, destination, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := update.rollback(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "old")); err != nil || string(data) != "old version" {
		t.Fatalf("restored directory = %q, error = %v", data, err)
	}
}

func TestManagedMinerBundleExtractionRejectsUnsafeEntries(t *testing.T) {
	tests := []agentBundleEntry{
		{name: "../miner", body: []byte("bad")},
		{name: `bundle\miner`, body: []byte("bad")},
		{name: "bundle/miner", typeflag: tar.TypeSymlink, linkname: "/bin/sh"},
		{name: "bundle/miner", typeflag: tar.TypeLink, linkname: "other"},
		{name: "bundle/miner", typeflag: tar.TypeBlock},
	}
	for index, entry := range tests {
		archivePath := filepath.Join(t.TempDir(), "unsafe.tar.gz")
		writeAgentBundle(t, archivePath, []agentBundleEntry{entry})
		if err := extractManagedMinerBundle(archivePath, filepath.Join(t.TempDir(), "output")); err == nil {
			t.Fatalf("unsafe bundle %d was accepted", index)
		}
	}
}

func TestManagedMinerBundleRejectsHashMismatches(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "source.tar.gz")
	entry := []byte("miner")
	writeAgentBundle(t, archivePath, []agentBundleEntry{{name: "bundle/miner", body: entry, mode: 0o755}})
	bundle, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	packageDigest := sha256.Sum256(bundle)
	entryDigest := sha256.Sum256(entry)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bundle)
	}))
	defer server.Close()
	client := &Client{
		config:   Config{Controller: server.URL, PackageDir: root},
		identity: Identity{AgentID: "rig", Token: "token"},
		http:     server.Client(),
	}
	t.Run("package", func(t *testing.T) {
		if _, err := client.downloadMinerBundle(context.Background(), "miner", filepath.Join(root, "package-mismatch"), "bundle/miner", strings.Repeat("0", 64), hex.EncodeToString(entryDigest[:])); err == nil {
			t.Fatal("package hash mismatch was accepted")
		}
	})
	t.Run("entry point", func(t *testing.T) {
		if _, err := client.downloadMinerBundle(context.Background(), "miner", filepath.Join(root, "entry-mismatch"), "bundle/miner", hex.EncodeToString(packageDigest[:]), strings.Repeat("0", 64)); err == nil {
			t.Fatal("entry-point hash mismatch was accepted")
		}
	})
}

type agentBundleEntry struct {
	name     string
	body     []byte
	mode     int64
	typeflag byte
	linkname string
}

func writeAgentBundle(t *testing.T, filename string, entries []agentBundleEntry) {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		size := int64(len(entry.body))
		if typeflag != tar.TypeReg && typeflag != tar.TypeRegA {
			size = 0
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Mode: mode, Size: size, Typeflag: typeflag, Linkname: entry.linkname}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := archive.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
