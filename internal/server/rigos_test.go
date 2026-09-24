package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"minerdash/internal/protocol"
)

func TestRigOSCatalogInstallAndImageStatus(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(filepath.Join(root, "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	status := store.RigOSStatus()
	if status.ImageAvailable || len(status.Miners) < 10 {
		t.Fatalf("initial Rig OS status = %#v", status)
	}
	miner, err := store.InstallCatalogMiner("rigel")
	if err != nil {
		t.Fatal(err)
	}
	if miner.CatalogID != "rigel" || !miner.ManagedBinary || miner.BinaryName != "rigel" {
		t.Fatalf("installed catalog miner = %#v", miner)
	}
	again, err := store.InstallCatalogMiner("rigel")
	if err != nil || again.ID != miner.ID || len(store.ListMiners()) != 1 {
		t.Fatalf("idempotent install = %#v, error = %v", again, err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, installErr := store.InstallCatalogMiner("wildrig-multi"); installErr != nil {
				t.Errorf("concurrent catalog install: %v", installErr)
			}
		}()
	}
	wait.Wait()
	wildRigCount := 0
	for _, installed := range store.ListMiners() {
		if installed.CatalogID == "wildrig-multi" {
			wildRigCount++
		}
	}
	if wildRigCount != 1 {
		t.Fatalf("concurrent catalog installs created %d definitions", wildRigCount)
	}
	miner.Version = "1.0.0"
	miner.CatalogID = ""
	updated, err := store.SaveMiner(miner)
	if err != nil || updated.CatalogID != "rigel" {
		t.Fatalf("catalog identity after edit = %#v, error = %v", updated, err)
	}
	if _, err := store.SaveMinerBinary(updated.ID, bytes.NewBufferString("reviewed package")); err != nil {
		t.Fatal(err)
	}
	status = store.RigOSStatus()
	if !status.Miners[0].PackageReady || status.Miners[0].InstalledVersion != "1.0.0" {
		t.Fatalf("catalog package status = %#v", status.Miners[0])
	}
	xmrig, err := store.InstallCatalogMiner("xmrig")
	if err != nil {
		t.Fatal(err)
	}
	if xmrig.ManagedBinary || xmrig.Profile != "xmrig" {
		t.Fatalf("XMRig catalog definition = %#v", xmrig)
	}
	miniZ, err := store.SaveMiner(protocol.MinerDefinition{
		Name: "miniZ", Version: "2.5e3", Algorithm: "equihash192_7",
		ManagedBinary: true, BinaryName: "miniZ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMinerBinary(miniZ.ID, bytes.NewBufferString("verified miniZ package")); err != nil {
		t.Fatal(err)
	}
	status = store.RigOSStatus()
	var miniZCatalog protocol.MinerCatalogEntry
	for _, entry := range status.Miners {
		if entry.ID == "miniz" {
			miniZCatalog = entry
			break
		}
	}
	if miniZCatalog.InstalledMinerID != miniZ.ID || miniZCatalog.InstalledVersion != "2.5e3" || !miniZCatalog.PackageReady {
		t.Fatalf("miniZ catalog package status = %#v", miniZCatalog)
	}

	imageDirectory := filepath.Join(root, "rig-os")
	if err := os.MkdirAll(imageDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imageDirectory, rigOSImageFilename), []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(imageDirectory, rigOSImageFilename+".sha256"), []byte(digest+"  "+rigOSImageFilename+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status = store.RigOSStatus()
	if !status.ImageAvailable || status.ImageSHA256 != digest || status.ImageSizeBytes != 5 {
		t.Fatalf("published Rig OS status = %#v", status)
	}
}

func TestRigOSHTTPRequiresAuthenticationAndServesImage(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(filepath.Join(root, "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	imageDirectory := filepath.Join(root, "rig-os")
	if err := os.MkdirAll(imageDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imageDirectory, rigOSImageFilename), []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imageDirectory, rigOSImageFilename+".sha256"), []byte(strings.Repeat("b", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPServer(store, log.New(testWriter{t}, "", 0))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/rig-os", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/rig-os/miners/xmrig", nil)
	request.Header.Set("Authorization", "Bearer admin")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("catalog install status = %d, body = %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/rig-os/image?ticket=invalid", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid image ticket status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/rig-os/image-ticket", nil)
	request.Header.Set("Authorization", "Bearer admin")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("ticket status = %d, body = %q", response.Code, response.Body.String())
	}
	var ticket struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, ticket.URL, nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "image" {
		t.Fatalf("image status = %d, body = %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, ticket.URL, nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("reused image ticket status = %d", response.Code)
	}
}
