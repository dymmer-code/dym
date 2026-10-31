package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppMemoryCLIIsLocalAndReportsDeniedWithoutSecrets(t *testing.T) {
	for _, args := range [][]string{{"app", "memory", "--manager", "systemctl", "--service", "demo"}, {"app", "memory", "--manager", "systemctl", "--service", "demo", "--path", "/tmp"}, {"app", "memory"}} {
		var out bytes.Buffer
		cmd := NewRootCommand(Dependencies{Out: &out, Err: &out})
		cfg := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(cfg, []byte("logs: {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--config", cfg)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if len(args) == 8 {
			var r map[string]any
			if err != nil || json.Unmarshal(out.Bytes(), &r) != nil || r["status"] != "permission_denied" {
				t.Fatalf("%s %v", out.String(), err)
			}
		} else if err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestAppMemoryCLIInvalidPolicy(t *testing.T) {
	var out bytes.Buffer
	cfg := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(cfg, []byte("unknown_field: secret\n"), 0600)
	cmd := NewRootCommand(Dependencies{Out: &out, Err: &out})
	cmd.SetArgs([]string{"app", "memory", "--manager", "docker", "--service", "demo", "--config", cfg})
	if err := cmd.Execute(); err == nil {
		t.Fatal("accepted invalid config")
	}
}
