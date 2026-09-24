package server

import (
	"errors"
	"path/filepath"
	"testing"

	"minerdash/internal/protocol"
)

func TestUserAuthenticationAndLastAdminProtection(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "bootstrap", "enroll")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.SaveUser("", protocol.UserInput{Username: "admin", Password: "short", Role: "admin"}); err == nil {
		t.Fatal("SaveUser() accepted a short password")
	}
	user, err := store.SaveUser("", protocol.UserInput{Username: "admin", Password: "a-long-test-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateUser("admin", "wrong-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateUser() error = %v, want unauthorized", err)
	}
	authenticated, err := store.AuthenticateUser("ADMIN", "a-long-test-password")
	if err != nil || authenticated.ID != user.ID {
		t.Fatalf("AuthenticateUser() user = %#v, error = %v", authenticated, err)
	}
	if _, err := store.SaveUser(user.ID, protocol.UserInput{Username: "admin", Role: "viewer"}); err == nil {
		t.Fatal("SaveUser() demoted the last administrator")
	}
	if err := store.DeleteUser(user.ID); err == nil {
		t.Fatal("DeleteUser() deleted the last administrator")
	}
}

func TestResetUserPasswordWithRecoveryCode(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), "bootstrap", "enroll")
	if err != nil {
		t.Fatal(err)
	}
	store.SetRecoveryCode("local-recovery-code")
	user, err := store.SaveUser("", protocol.UserInput{Username: "admin", Password: "original-password-123", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResetUserPassword(user.Username, "replacement-password-123", "wrong-code"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong recovery code error = %v, want unauthorized", err)
	}
	if _, err := store.ResetUserPassword(user.Username, "replacement-password-123", "local-recovery-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateUser(user.Username, "original-password-123"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old password error = %v, want unauthorized", err)
	}
	if _, err := store.AuthenticateUser(user.Username, "replacement-password-123"); err != nil {
		t.Fatalf("new password authentication failed: %v", err)
	}
}
