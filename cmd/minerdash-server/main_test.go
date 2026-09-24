package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardAddress(t *testing.T) {
	tests := map[string]string{
		":8443":          "localhost:8443",
		"127.0.0.1:8443": "127.0.0.1:8443",
	}

	for input, expected := range tests {
		if actual := dashboardAddress(input); actual != expected {
			t.Errorf("dashboardAddress(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestLoadOrCreateSecretsAddsRecoveryCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	data, err := json.Marshal(secrets{AdminToken: "admin", EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadOrCreateSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RecoveryCode == "" {
		t.Fatal("missing generated recovery code")
	}
	reloaded, err := loadOrCreateSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RecoveryCode != loaded.RecoveryCode {
		t.Fatal("recovery code was not persisted")
	}
}
