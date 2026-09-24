package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigRequiresAbsoluteMinerCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	data := `{
		"controller":"https://controller:8443",
		"tls_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"name":"rig-01",
		"state_file":"/var/lib/minerdash/identity.json",
		"profiles":{"qrl":{"command":"xmrig","args":[]}}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig() accepted a relative miner command")
	}
}

func TestMinerManagerRejectsArbitraryActions(t *testing.T) {
	manager := NewMinerManager(nil)
	if err := manager.Execute("shell", "anything"); err == nil {
		t.Fatal("Execute() accepted an arbitrary action")
	}
}
