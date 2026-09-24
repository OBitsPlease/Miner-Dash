package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"minerdash/internal/protocol"
)

func TestHTTPEnrollmentAndAdminAuthentication(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPServer(store, log.New(testWriter{t}, "", 0))
	body, _ := json.Marshal(protocol.EnrollRequest{Name: "rig-01", EnrollmentToken: "enroll"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/enroll", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("enrollment status = %d, body = %s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/rigs", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/rigs", nil)
	request.Header.Set("Authorization", "Bearer admin")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, body = %s", response.Code, response.Body)
	}
}

func TestRemoteIPAddress(t *testing.T) {
	tests := map[string]string{
		"192.0.2.10:4321":   "192.0.2.10",
		"[2001:db8::10]:80": "2001:db8::10",
		"192.0.2.11":        "192.0.2.11",
		"not-an-address":    "",
	}
	for remoteAddress, expected := range tests {
		if actual := remoteIPAddress(remoteAddress); actual != expected {
			t.Errorf("remoteIPAddress(%q) = %q, want %q", remoteAddress, actual, expected)
		}
	}
}

func TestHTTPPasswordLoginHonorsRoles(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "bootstrap", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.SaveUser("", protocol.UserInput{Username: "viewer", Password: "viewer-password-123", Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPServer(store, log.New(testWriter{t}, "", 0))
	body, _ := json.Marshal(protocol.LoginRequest{Username: "viewer", Password: "viewer-password-123"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/login", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body)
	}
	var login protocol.LoginResponse
	if err := json.Unmarshal(response.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/rigs", nil)
	request.Header.Set("Authorization", "Bearer "+login.Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("viewer read status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/farms", bytes.NewBufferString(`{"name":"denied"}`))
	request.Header.Set("Authorization", "Bearer "+login.Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("viewer write status = %d, want unauthorized", response.Code)
	}
	if _, err := store.SaveUser(user.ID, protocol.UserInput{Username: "viewer", Role: "viewer", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/rigs", nil)
	request.Header.Set("Authorization", "Bearer "+login.Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("disabled viewer status = %d, want unauthorized", response.Code)
	}
}

func TestHTTPDemotionRevokesOperatorAccess(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "bootstrap", "enroll")
	if err != nil {
		t.Fatal(err)
	}

	user, err := store.SaveUser("", protocol.UserInput{Username: "operator", Password: "operator-password-123", Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPServer(store, log.New(testWriter{t}, "", 0))
	body, _ := json.Marshal(protocol.LoginRequest{Username: "operator", Password: "operator-password-123"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/login", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body)
	}
	var login protocol.LoginResponse
	if err := json.Unmarshal(response.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveUser(user.ID, protocol.UserInput{Username: "operator", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/farms", bytes.NewBufferString(`{"name":"denied-after-demotion"}`))
	request.Header.Set("Authorization", "Bearer "+login.Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("demoted operator write status = %d, want unauthorized", response.Code)
	}
}

func TestHTTPPasswordRecovery(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "bootstrap", "enroll")
	if err != nil {
		t.Fatal(err)
	}

	store.SetRecoveryCode("local-recovery-code")
	if _, err := store.SaveUser("", protocol.UserInput{Username: "admin", Password: "original-password-123", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPServer(store, log.New(testWriter{t}, "", 0))
	body, _ := json.Marshal(protocol.PasswordRecoveryRequest{
		Username:     "admin",
		RecoveryCode: "local-recovery-code",
		NewPassword:  "replacement-password-123",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/password-recovery", bytes.NewReader(body))
	request.RemoteAddr = "192.0.2.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("recovery status = %d, body = %s", response.Code, response.Body)
	}
	if _, err := store.AuthenticateUser("admin", "replacement-password-123"); err != nil {
		t.Fatalf("recovered password authentication failed: %v", err)
	}
}

func TestAgentCanDownloadMatchingArchitectureRelease(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStore(filepath.Join(directory, "state.json"), "admin", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.Enroll("rig-01", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	releaseDirectory := filepath.Join(directory, "agent-releases")
	if err := os.MkdirAll(releaseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseDirectory, "minerdash-agent-amd64"), []byte("agent release"), 0o700); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPServer(store, log.New(testWriter{t}, "", 0))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+identity.AgentID+"/release/amd64", nil)
	request.Header.Set("Authorization", "Bearer "+identity.Token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "agent release" {
		t.Fatalf("agent release status = %d, body = %q", response.Code, response.Body.String())
	}
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(data []byte) (int, error) {
	w.t.Log(string(data))
	return len(data), nil
}
