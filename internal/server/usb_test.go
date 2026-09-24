package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestReadRigOSBuildManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest")
	hash := strings.Repeat("a", 64)
	if err := os.WriteFile(path, []byte("format=minerdash-raw-usb-v1\nsize_bytes=8589934592\noutput_sha256="+hash+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotHash, gotSize, err := readRigOSBuildManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != hash || gotSize != 8589934592 {
		t.Fatalf("manifest values = %q, %d", gotHash, gotSize)
	}
	if err := os.WriteFile(path, []byte("size_bytes=0\noutput_sha256=nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readRigOSBuildManifest(path); err == nil {
		t.Fatal("invalid manifest was accepted")
	}
}

func TestReadSingleHashAcceptsSidecarFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.sha256")
	hash := strings.Repeat("b", 64)
	if err := os.WriteFile(path, []byte(hash+"  image.img.xz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readSingleHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != hash {
		t.Fatalf("hash = %q", got)
	}
}

func TestUSBConfirmationAndFingerprint(t *testing.T) {
	device := usbDevice{DiskNumber: 7, Name: "Test USB", SerialNumber: "serial", SizeBytes: 16 << 30, uniqueID: "unique"}
	if usbEraseConfirmation(device) != "ERASE USB 7" {
		t.Fatalf("confirmation = %q", usbEraseConfirmation(device))
	}
	first := fingerprintUSBDevice(device)
	device.SizeBytes++
	if fingerprintUSBDevice(device) == first {
		t.Fatal("device fingerprint did not change with device identity")
	}
}

func TestValidateProvisioning(t *testing.T) {
	fingerprint := strings.Repeat("c", 64)
	if err := validateProvisioning("https://192.0.2.10:8443", "garage-rig-01", fingerprint); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		controller string
		name       string
	}{
		{controller: "http://192.0.2.10:8443", name: "rig"},
		{controller: "https://192.0.2.10", name: "rig"},
		{controller: "https://192.0.2.10:8443/path", name: "rig"},
		{controller: "https://192.0.2.10:8443", name: "invalid rig name"},
	} {
		if err := validateProvisioning(test.controller, test.name, fingerprint); err == nil {
			t.Fatalf("accepted controller %q and name %q", test.controller, test.name)
		}
	}
}

func TestEnsureRigOSImageDownloadsAndVerifiesRelease(t *testing.T) {
	raw := bytes.Repeat([]byte("MinerDash Rig OS"), 4096)
	rawDigest := sha256.Sum256(raw)
	var compressed bytes.Buffer
	writer, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	imageDigest := sha256.Sum256(compressed.Bytes())
	buildManifest := fmt.Sprintf("format=minerdash-raw-usb-v1\nsize_bytes=%d\noutput_sha256=%s\n", len(raw), hex.EncodeToString(rawDigest[:]))
	buildDigest := sha256.Sum256([]byte(buildManifest))
	checksums := fmt.Sprintf(
		`[{"Hash":%q,"File":%q},{"Hash":%q,"File":%q}]`,
		hex.EncodeToString(imageDigest[:]), rigOSImageFilename,
		hex.EncodeToString(buildDigest[:]), rigOSBuildManifestFilename,
	)
	var imageRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			fmt.Fprintf(w, `[{"tag_name":"v1.0.0","html_url":"%s/release","assets":[{"name":"SHA256SUMS.json","browser_download_url":"%s/checksums"},{"name":%q,"browser_download_url":"%s/image"},{"name":%q,"browser_download_url":"%s/build-manifest"}]}]`,
				server.URL, server.URL, rigOSImageFilename, server.URL, rigOSBuildManifestFilename, server.URL)
		case "/checksums":
			_, _ = io.WriteString(w, checksums)
		case "/image":
			imageRequests.Add(1)
			w.Header().Set("Content-Length", fmt.Sprint(compressed.Len()))
			_, _ = w.Write(compressed.Bytes())
		case "/build-manifest":
			_, _ = io.WriteString(w, buildManifest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	releases := newApplicationUpdateMonitor()
	releases.url = server.URL + "/releases"
	releases.client = server.Client()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	store.SetTLSFingerprint(strings.Repeat("c", 64))
	manager := newUSBManager(t.TempDir(), releases, store)
	manager.httpClient = server.Client()
	imagePath, hash, size, err := manager.ensureImage(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if hash != hex.EncodeToString(rawDigest[:]) || size != int64(len(raw)) {
		t.Fatalf("image metadata = %q, %d", hash, size)
	}
	if actual, err := packageFileSHA256(imagePath); err != nil || actual != hex.EncodeToString(imageDigest[:]) {
		t.Fatalf("downloaded image hash = %q, error = %v", actual, err)
	}
	if _, _, _, err := manager.ensureImage(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if imageRequests.Load() != 1 {
		t.Fatalf("image downloaded %d times; wanted cached verified image", imageRequests.Load())
	}
}
