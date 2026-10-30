package builderregistration

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterPreservesLocalConfigAndRetries(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "dymploy.yaml")
	os.WriteFile(config, []byte("listen: 127.0.0.1:8080\nrunner:\n  kind: docker\npolicy: []\n"), 0600)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/hosts/builder" || r.Header.Get("X-Dym-Host") != "host" || r.Header.Get("Authorization") != "Bearer permit" {
			t.Error("missing authentication")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["runner"]; ok {
			t.Error("local config leaked")
		}
		secret := strings.Repeat("a", 43)
		if calls > 1 {
			if r.Header.Get("X-Dym-Builder-Secret") != secret {
				t.Error("retry lost credential")
			}
			secret = ""
		}
		json.NewEncoder(w).Encode(map[string]any{"builder_id": "builder", "secret": secret})
	}))
	defer server.Close()
	options := Options{Config: config, Server: server.URL, HostID: "host", HostSecret: "host-secret", Token: "permit"}
	for range 2 {
		if _, err := Register(context.Background(), options); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(config)
	if !strings.Contains(string(raw), "kind: docker") || !strings.Contains(string(raw), "builder_id: builder") {
		t.Fatal(string(raw))
	}
	info, err := os.Stat(config + ".dymmer-token")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe secret file", err)
	}
}

func TestRejectsUnsafeServerAndInvalidYAML(t *testing.T) {
	for _, server := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?token=x"} {
		if _, err := Register(context.Background(), Options{Server: server}); err == nil {
			t.Fatal(server)
		}
	}
}

func TestRefusalDoesNotModifyFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dymploy.yaml")
	original := []byte("listen: 127.0.0.1:8080\n")
	os.WriteFile(path, original, 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret details must not be exposed", 403) }))
	defer server.Close()
	_, err := Register(context.Background(), Options{Config: path, Server: server.URL})
	if err == nil || strings.Contains(err.Error(), "secret details") {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(original) {
		t.Fatal("configuration changed")
	}
	if _, err = os.Stat(path + ".dymmer-token"); !os.IsNotExist(err) {
		t.Fatal("credential created")
	}
}

func TestRejectsSymlinkAndMalformedConfigurationBeforeNetwork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("listen: 127.0.0.1:8080\n"), 0600)
	if err := os.Symlink(target, path); err == nil {
		if _, err = Register(context.Background(), Options{Config: path, Server: "http://127.0.0.1"}); err == nil {
			t.Fatal("accepted symlink")
		}
		os.Remove(path)
	}
	for _, data := range []string{"[", "[]", "listen: a\nlisten: b\n", "control_plane: wrong\n", "policy: wrong\n", "listen: bad\n"} {
		os.WriteFile(path, []byte(data), 0600)
		if _, err := Register(context.Background(), Options{Config: path, Server: "http://127.0.0.1"}); err == nil {
			t.Fatal(data)
		}
	}
}

func TestPrivateCAIsExplicitAndTLSRemainsVerified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("listen: 127.0.0.1:8080\n"), 0600)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"builder_id": "tls-builder", "secret": strings.Repeat("b", 43)})
	}))
	defer server.Close()
	options := Options{Config: path, Server: server.URL}
	if _, err := Register(context.Background(), options); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
	ca := filepath.Join(dir, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	options.CAFile = ca
	if _, err := Register(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}
