package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type updateConfig struct {
	service        string
	server         string
	stagedServer   string
	agentDirectory string
	stagedAMD64    string
	stagedARM64    string
	healthURL      string
	logPath        string
}

func main() {
	config := updateConfig{}
	flag.StringVar(&config.service, "service", "MinerDash", "Windows service name")
	flag.StringVar(&config.server, "server", "", "installed controller executable")
	flag.StringVar(&config.stagedServer, "staged-server", "", "verified replacement controller")
	flag.StringVar(&config.agentDirectory, "agent-directory", "", "controller agent release directory")
	flag.StringVar(&config.stagedAMD64, "staged-amd64", "", "verified AMD64 agent")
	flag.StringVar(&config.stagedARM64, "staged-arm64", "", "verified ARM64 agent")
	flag.StringVar(&config.healthURL, "health", "https://localhost:8443/healthz", "controller health URL")
	flag.StringVar(&config.logPath, "log", "", "update log path")
	flag.Parse()
	if err := run(config); err != nil {
		appendLog(config.logPath, "update failed: "+err.Error())
		os.Exit(1)
	}
}

func run(config updateConfig) error {
	if config.server == "" || config.stagedServer == "" || config.agentDirectory == "" ||
		config.stagedAMD64 == "" || config.stagedARM64 == "" {
		return errors.New("update paths are incomplete")
	}
	for _, filename := range []string{config.server, config.stagedServer, config.stagedAMD64, config.stagedARM64} {
		if filepath.Clean(filename) != filename || !filepath.IsAbs(filename) {
			return fmt.Errorf("update path must be absolute and normalized: %s", filename)
		}
	}
	time.Sleep(2 * time.Second)
	appendLog(config.logPath, "stopping controller service")
	_ = runSC("stop", config.service)
	if err := waitForService(config.service, "STOPPED", 45*time.Second); err != nil {
		return err
	}
	serverBackup := config.server + ".previous"
	if err := os.Remove(serverBackup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := copyFile(config.server, serverBackup, 0o700); err != nil {
		return fmt.Errorf("back up controller: %w", err)
	}
	if err := copyFile(config.stagedServer, config.server, 0o700); err != nil {
		return fmt.Errorf("replace controller: %w", err)
	}
	if err := os.MkdirAll(config.agentDirectory, 0o700); err != nil {
		return err
	}
	if err := copyFile(config.stagedAMD64, filepath.Join(config.agentDirectory, "minerdash-agent-amd64"), 0o700); err != nil {
		return fmt.Errorf("publish AMD64 agent: %w", err)
	}
	if err := copyFile(config.stagedARM64, filepath.Join(config.agentDirectory, "minerdash-agent-arm64"), 0o700); err != nil {
		return fmt.Errorf("publish ARM64 agent: %w", err)
	}
	appendLog(config.logPath, "starting updated controller")
	if err := runSC("start", config.service); err != nil {
		return rollback(config, serverBackup, err)
	}
	if err := waitForHealth(config.healthURL, 60*time.Second); err != nil {
		return rollback(config, serverBackup, err)
	}
	_ = os.Remove(serverBackup)
	appendLog(config.logPath, "update completed successfully")
	return nil
}

func rollback(config updateConfig, backup string, cause error) error {
	appendLog(config.logPath, "updated controller failed; restoring previous version")
	_ = runSC("stop", config.service)
	_ = waitForService(config.service, "STOPPED", 30*time.Second)
	if err := copyFile(backup, config.server, 0o700); err != nil {
		return fmt.Errorf("%v; restore previous controller: %w", cause, err)
	}
	if err := runSC("start", config.service); err != nil {
		return fmt.Errorf("%v; restart previous controller: %w", cause, err)
	}
	return fmt.Errorf("updated controller failed health check and was rolled back: %w", cause)
}

func runSC(arguments ...string) error {
	output, err := exec.Command("sc.exe", arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc.exe %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func waitForService(service, desired string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		output, _ := exec.Command("sc.exe", "query", service).CombinedOutput()
		if strings.Contains(string(output), desired) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("service %s did not reach %s", service, desired)
}

func waitForHealth(url string, timeout time.Duration) error {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		}},
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return errors.New("controller health check timed out")
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temp := destination + ".update"
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, destination); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func appendLog(filename, message string) {
	if filename == "" {
		return
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().UTC().Format(time.RFC3339), message)
}
