package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

const passwordIterations = 210_000

type storedUser struct {
	protocol.User
	Salt         string `json:"salt"`
	PasswordHash string `json:"password_hash"`
}

func (s *Store) ListUsers() []protocol.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.User, 0, len(s.state.Users))
	for _, user := range s.state.Users {
		result = append(result, user.User)
	}
	return result
}

func (s *Store) UserByID(id string) (protocol.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.state.Users[id]
	if !ok {
		return protocol.User{}, false
	}
	return user.User, true
}

func (s *Store) SaveUser(id string, input protocol.UserInput) (protocol.User, error) {
	input.Username = strings.TrimSpace(input.Username)
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	if input.Username == "" {
		return protocol.User{}, errors.New("username is required")
	}
	if input.Role != "admin" && input.Role != "operator" && input.Role != "viewer" {
		return protocol.User{}, errors.New("role must be admin, operator, or viewer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for existingID, existing := range s.state.Users {
		if existingID != id && strings.EqualFold(existing.Username, input.Username) {
			return protocol.User{}, errors.New("username already exists")
		}
	}
	var user *storedUser
	if id == "" {
		if len(input.Password) < 12 {
			return protocol.User{}, errors.New("new users require a password of at least 12 characters")
		}
		id = mustToken(12)
		user = &storedUser{User: protocol.User{ID: id, CreatedAt: time.Now().UTC()}}
	} else {
		existing, ok := s.state.Users[id]
		if !ok {
			return protocol.User{}, ErrNotFound
		}
		if existing.Role == "admin" && !existing.Disabled && (input.Role != "admin" || input.Disabled) && s.enabledAdminCountLocked() <= 1 {
			return protocol.User{}, errors.New("cannot disable or demote the last enabled administrator")
		}
		copy := *existing
		user = &copy
	}
	if input.Password != "" {
		if len(input.Password) < 12 {
			return protocol.User{}, errors.New("password must be at least 12 characters")
		}
		salt, err := randomToken(16)
		if err != nil {
			return protocol.User{}, err
		}
		user.Salt = salt
		user.PasswordHash = hex.EncodeToString(derivePassword([]byte(input.Password), []byte(salt), passwordIterations, 32))
	}
	user.Username = input.Username
	user.Role = input.Role
	user.Disabled = input.Disabled
	s.state.Users[id] = user
	s.recordLocked("save", "user", id, map[string]any{"username": user.Username, "role": user.Role, "disabled": user.Disabled})
	return user.User, s.saveLocked()
}

func (s *Store) AuthenticateUser(username, password string) (protocol.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, user := range s.state.Users {
		if !strings.EqualFold(user.Username, strings.TrimSpace(username)) {
			continue
		}
		candidate := hex.EncodeToString(derivePassword([]byte(password), []byte(user.Salt), passwordIterations, 32))
		if user.Disabled || subtle.ConstantTimeCompare([]byte(candidate), []byte(user.PasswordHash)) != 1 {
			return protocol.User{}, ErrUnauthorized
		}
		return user.User, nil
	}
	// Run the derivation even for unknown users to reduce username timing differences.
	_ = derivePassword([]byte(password), []byte("unknown-user-salt"), passwordIterations, 32)
	return protocol.User{}, ErrUnauthorized
}

func (s *Store) ResetUserPassword(username, password, recoveryCode string) (protocol.User, error) {
	if len(password) < 12 {
		return protocol.User{}, errors.New("password must be at least 12 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if recoveryCode == "" || subtle.ConstantTimeCompare([]byte(hashToken(recoveryCode)), []byte(s.recoveryHash)) != 1 {
		return protocol.User{}, ErrUnauthorized
	}
	var user *storedUser
	for _, candidate := range s.state.Users {
		if strings.EqualFold(candidate.Username, strings.TrimSpace(username)) {
			user = candidate
			break
		}
	}
	if user == nil {
		return protocol.User{}, ErrUnauthorized
	}
	salt, err := randomToken(16)
	if err != nil {
		return protocol.User{}, err
	}
	user.Salt = salt
	user.PasswordHash = hex.EncodeToString(derivePassword([]byte(password), []byte(salt), passwordIterations, 32))
	s.recordLocked("recover", "user", user.ID, map[string]any{"username": user.Username})
	return user.User, s.saveLocked()
}

func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.state.Users[id]
	if !ok {
		return ErrNotFound
	}
	if user.Role == "admin" && !user.Disabled {
		admins := 0
		for _, candidate := range s.state.Users {
			if candidate.Role == "admin" && !candidate.Disabled {
				admins++
			}
		}
		if admins <= 1 {
			return errors.New("cannot delete the last enabled administrator")
		}
	}
	delete(s.state.Users, id)
	s.recordLocked("delete", "user", id, map[string]any{"username": user.Username})
	return s.saveLocked()
}

func (s *Store) enabledAdminCountLocked() int {
	count := 0
	for _, user := range s.state.Users {
		if user.Role == "admin" && !user.Disabled {
			count++
		}
	}
	return count
}

func derivePassword(password, salt []byte, iterations, length int) []byte {
	hashLength := sha256.Size
	blocks := (length + hashLength - 1) / hashLength
	result := make([]byte, 0, blocks*hashLength)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		value := append([]byte(nil), u...)
		for iteration := 1; iteration < iterations; iteration++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for index := range value {
				value[index] ^= u[index]
			}
		}
		result = append(result, value...)
	}
	return result[:length]
}
