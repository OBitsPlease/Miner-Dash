package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minerdash/internal/protocol"
)

func TestManagedMinerUploadAndAuthorization(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.SaveMiner(protocol.MinerDefinition{
		Name: "Reviewed Miner", Algorithm: "test", ManagedBinary: true, BinaryName: "miner",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("test executable")
	miner, err = store.SaveMinerBinary(miner.ID, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(miner.BinarySHA256) != 64 {
		t.Fatalf("binary hash = %q", miner.BinarySHA256)
	}
	identity, err := store.Enroll("rig", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.MinerBinaryForRig(identity.AgentID, miner.ID); err == nil {
		t.Fatal("unassigned rig accessed miner binary")
	}
	wallet, _ := store.SaveWallet(protocol.Wallet{Name: "Wallet", Coin: "TEST", Address: "address"})
	pool, _ := store.SavePool(protocol.Pool{Name: "Pool", URL: "stratum+tcp://pool.invalid:1"})
	sheet, _ := store.SaveFlightSheet(protocol.FlightSheet{Name: "Sheet", WalletID: wallet.ID, PoolID: pool.ID, MinerID: miner.ID})
	if _, err := store.AssignWorker(identity.AgentID, protocol.WorkerAssignment{FlightSheetID: sheet.ID}); err != nil {
		t.Fatal(err)
	}
	filename, assigned, err := store.MinerBinaryForRig(identity.AgentID, miner.ID)
	if err != nil || assigned.ID != miner.ID || filepath.Base(filename) != miner.ID+".bin" {
		t.Fatalf("MinerBinaryForRig() path = %q, miner = %#v, error = %v", filename, assigned, err)
	}
}

func TestManagedMinerBundleUploadAndValidation(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.SaveMiner(protocol.MinerDefinition{
		Name: "Bundle Miner", Algorithm: "test", ManagedBinary: true,
		PackageFormat: "tar.gz", EntryPoint: "bundle/miner", BinaryName: "miner",
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := []byte("bundle executable")
	bundle := testMinerBundle(t, []testBundleEntry{
		{name: "bundle/", mode: 0o755, typeflag: tar.TypeDir},
		{name: "bundle/miner", body: entry, mode: 0o755},
		{name: "bundle/libhelper.so", body: []byte("library"), mode: 0o644},
	})
	saved, err := store.SaveMinerBundle(miner.ID, bytes.NewReader(bundle))
	if err != nil {
		t.Fatal(err)
	}
	entryHash := sha256.Sum256(entry)
	packageHash := sha256.Sum256(bundle)
	if saved.BinarySHA256 != hex.EncodeToString(entryHash[:]) {
		t.Fatalf("entry hash = %q", saved.BinarySHA256)
	}
	if saved.PackageSHA256 != hex.EncodeToString(packageHash[:]) {
		t.Fatalf("package hash = %q", saved.PackageSHA256)
	}
	if _, err := os.Stat(filepath.Join(store.packageDir, miner.ID+".tar.gz")); err != nil {
		t.Fatal(err)
	}
}

func TestManagedMinerBundleRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry testBundleEntry
	}{
		{name: "traversal", entry: testBundleEntry{name: "../miner", body: []byte("bad")}},
		{name: "backslash", entry: testBundleEntry{name: `bundle\miner`, body: []byte("bad")}},
		{name: "symlink", entry: testBundleEntry{name: "bundle/miner", typeflag: tar.TypeSymlink, linkname: "/bin/sh"}},
		{name: "hardlink", entry: testBundleEntry{name: "bundle/miner", typeflag: tar.TypeLink, linkname: "other"}},
		{name: "device", entry: testBundleEntry{name: "bundle/miner", typeflag: tar.TypeChar}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := testMinerBundle(t, []testBundleEntry{test.entry})
			filename := filepath.Join(t.TempDir(), "bundle.tar.gz")
			if err := os.WriteFile(filename, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := validateMinerBundle(filename, "bundle/miner"); err == nil {
				t.Fatal("unsafe bundle was accepted")
			}
		})
	}
}

func TestManagedMinerBundleRequiresEntryPoint(t *testing.T) {
	archive := testMinerBundle(t, []testBundleEntry{{name: "bundle/other", body: []byte("other")}})
	filename := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(filename, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateMinerBundle(filename, "bundle/miner"); err == nil || !strings.Contains(err.Error(), "entry point") {
		t.Fatalf("missing entry-point error = %v", err)
	}
}

func TestManagedMinerBundleHashMismatchPreservesInstalledPackage(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	miner, err := store.SaveMiner(protocol.MinerDefinition{
		Name: "Bundle Miner", Algorithm: "test", ManagedBinary: true,
		PackageFormat: "tar.gz", EntryPoint: "bundle/miner", BinaryName: "miner",
	})
	if err != nil {
		t.Fatal(err)
	}
	original := testMinerBundle(t, []testBundleEntry{{name: "bundle/miner", body: []byte("old")}})
	saved, err := store.SaveMinerBundle(miner.ID, bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	replacement := testMinerBundle(t, []testBundleEntry{{name: "bundle/miner", body: []byte("new")}})
	if _, err := store.saveMinerBundle(miner.ID, bytes.NewReader(replacement), strings.Repeat("0", 64), ""); err == nil {
		t.Fatal("package hash mismatch was accepted")
	}
	installed, err := os.ReadFile(filepath.Join(store.packageDir, miner.ID+".tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, original) {
		t.Fatal("installed package changed after hash mismatch")
	}
	if current := store.ListMiners()[0]; current.PackageSHA256 != saved.PackageSHA256 {
		t.Fatalf("stored package hash changed from %q to %q", saved.PackageSHA256, current.PackageSHA256)
	}
}

type testBundleEntry struct {
	name     string
	body     []byte
	mode     int64
	typeflag byte
	linkname string
}

func testMinerBundle(t *testing.T, entries []testBundleEntry) []byte {
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
	return output.Bytes()
}
