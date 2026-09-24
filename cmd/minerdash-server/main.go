package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"minerdash/internal/server"
)

type secrets struct {
	AdminToken      string `json:"admin_token"`
	EnrollmentToken string `json:"enrollment_token"`
	RecoveryCode    string `json:"recovery_code"`
}

type appConfig struct {
	address string
	dataDir string
}

func main() {
	address := flag.String("listen", ":8443", "HTTPS listen address")
	dataDir := flag.String("data", "data", "persistent data directory")
	flag.Parse()
	config := appConfig{address: *address, dataDir: *dataDir}
	logger := log.New(os.Stdout, "minerdash: ", log.LstdFlags|log.LUTC)
	handled, err := runAsWindowsService("MinerDash", func(ctx context.Context) error {
		return runController(ctx, config, logger)
	})
	if err != nil {
		logger.Fatal(err)
	}
	if handled {
		return
	}
	if err := runController(context.Background(), config, logger); err != nil {
		logger.Fatal(err)
	}
}

func runController(ctx context.Context, config appConfig, logger *log.Logger) error {
	secretValues, err := loadOrCreateSecrets(filepath.Join(config.dataDir, "secrets.json"))
	if err != nil {
		return err
	}
	store, err := server.NewStore(filepath.Join(config.dataDir, "state.json"), secretValues.AdminToken, secretValues.EnrollmentToken)
	if err != nil {
		return err
	}
	store.SetRecoveryCode(secretValues.RecoveryCode)
	certFile := filepath.Join(config.dataDir, "tls.crt")
	keyFile := filepath.Join(config.dataDir, "tls.key")
	if err := server.EnsureCertificate(certFile, keyFile); err != nil {
		return err
	}
	fingerprint, err := server.CertificateFingerprint(certFile)
	if err != nil {
		return err
	}
	store.SetTLSFingerprint(fingerprint)
	logger.Printf("dashboard: https://%s", dashboardAddress(config.address))
	logger.Printf("admin token: %s", secretValues.AdminToken)
	logger.Printf("agent enrollment token: %s", secretValues.EnrollmentToken)
	logger.Printf("TLS certificate SHA-256: %s", fingerprint)
	go runScheduler(ctx, store, logger)
	httpServer := &http.Server{
		Addr:              config.address,
		Handler:           server.NewHTTPServer(store, logger),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	result := make(chan error, 1)
	go func() {
		result <- httpServer.ListenAndServeTLS(certFile, keyFile)
	}()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func runScheduler(ctx context.Context, store *server.Store, logger *log.Logger) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := store.RunSchedulesAt(now); err != nil {
				logger.Printf("schedule evaluation failed: %v", err)
			}
			if err := store.EvaluateWorkerConnectivity(now); err != nil {
				logger.Printf("worker connectivity evaluation failed: %v", err)
			}
		}
	}
}

func dashboardAddress(address string) string {
	if len(address) > 0 && address[0] == ':' {
		return "localhost" + address
	}
	return address
}

func loadOrCreateSecrets(path string) (secrets, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var result secrets
		if err := json.Unmarshal(data, &result); err != nil {
			return secrets{}, err
		}
		if result.AdminToken == "" || result.EnrollmentToken == "" {
			return secrets{}, errors.New("secrets file is incomplete")
		}
		if result.RecoveryCode == "" {
			result.RecoveryCode, err = randomHex(16)
			if err != nil {
				return secrets{}, err
			}
			if err := writeSecrets(path, result); err != nil {
				return secrets{}, err
			}
		}
		return result, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return secrets{}, err
	}
	admin, err := randomHex(32)
	if err != nil {
		return secrets{}, err
	}
	enrollment, err := randomHex(32)
	if err != nil {
		return secrets{}, err
	}
	recovery, err := randomHex(16)
	if err != nil {
		return secrets{}, err
	}
	result := secrets{AdminToken: admin, EnrollmentToken: enrollment, RecoveryCode: recovery}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return secrets{}, err
	}
	return result, writeSecrets(path, result)
}

func writeSecrets(path string, values secrets) error {
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", value), nil
}
