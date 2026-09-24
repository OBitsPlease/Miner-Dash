package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"minerdash/internal/server"
)

func TestAgentEnrollsAndReportsHeartbeat(t *testing.T) {
	store, err := server.NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	controller := httptest.NewTLSServer(server.NewHTTPServer(store, log.New(io.Discard, "", 0)))
	defer controller.Close()
	certificate := controller.Certificate()
	fingerprint := sha256.Sum256(certificate.Raw)
	client, err := NewClient(Config{
		Controller: controller.URL, TLSFingerprint: hex.EncodeToString(fingerprint[:]),
		EnrollmentToken: "enroll", Name: "integration-rig",
		StateFile:  filepath.Join(t.TempDir(), "identity.json"),
		PackageDir: filepath.Join(t.TempDir(), "packages"),
		Profiles:   make(map[string]Profile),
	}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := client.enroll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client.identity = identity
	if err := client.heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	rigs := store.ListRigs(time.Now().UTC())
	if len(rigs) != 1 || !rigs[0].Online || rigs[0].Name != "integration-rig" {
		t.Fatalf("reported rigs = %#v", rigs)
	}
}
