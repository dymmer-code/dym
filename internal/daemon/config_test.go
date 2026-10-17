package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const fullConfig = `
privilege: [sudo, -n]
services:
  systemctl: [nginx, "app@1.service"]
  docker: [web]
compose:
  - path: /srv/app
    services: [web, worker]
actions:
  deploy-site:
    command: [/usr/local/bin/deploy-site, --prod]
    dir: /srv/site
    env: {MODE: prod}
    timeout: 5m
    privileged: true
logs:
  files: [/var/log/nginx/access.log]
  units: [nginx]
`

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, fullConfig))
	if err != nil {
		t.Fatal(err)
	}
	if !c.AllowsService("systemctl", "app@1.service") || c.AllowsService("systemctl", "sshd") || c.AllowsService("docker", "nginx") {
		t.Errorf("services: %+v", c.Services)
	}
	if p, found := c.ComposeProject("/srv/app"); !found || len(p.Services) != 2 {
		t.Errorf("compose: %+v", c.Compose)
	}
	if d, _ := c.Actions["deploy-site"].Duration(); d != 5*time.Minute {
		t.Errorf("timeout %s", d)
	}
	if got := c.ActionNames(); len(got) != 1 || got[0] != "deploy-site" {
		t.Errorf("actions %v", got)
	}
}

func TestNoFileAnEmptyFileOrOnlyCommentsAreNothingAllowed(t *testing.T) {
	for name, path := range map[string]string{
		"absent":   filepath.Join(t.TempDir(), "none.yaml"),
		"empty":    writeConfig(t, ""),
		"comments": writeConfig(t, "# nothing here yet\n"),
	} {
		c, err := LoadConfig(path)
		if err != nil || c.AllowsService("systemctl", "nginx") || len(c.Actions) != 0 {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
}

func TestAWrongFileIsRefusedWholeAndNothingOfItIsUsed(t *testing.T) {
	for name, text := range map[string]string{
		"a typo in a key":           "servises:\n  systemctl: [nginx]\n",
		"not yaml":                  "services: [",
		"an unknown manager":        "services:\n  rc: [x]\n",
		"a name with a space":       "services:\n  systemctl: ['a b']\n",
		"a name with a dash":        "services:\n  docker: ['-rf']\n",
		"a relative compose":        "compose:\n  - path: srv/app\n",
		"an unclean path":           "compose:\n  - path: /srv/app/../x\n",
		"an action with no command": "actions:\n  x: {dir: /tmp}\n",
		"an action named badly":     "actions:\n  'a b': {command: [/bin/true]}\n",
		"a bad timeout":             "actions:\n  x: {command: [/bin/true], timeout: soon}\n",
		"a timeout too long":        "actions:\n  x: {command: [/bin/true], timeout: 3h}\n",
		"privileged without it":     "actions:\n  x: {command: [/bin/true], privileged: true}\n",
		"a relative log":            "logs:\n  files: [var/log/x]\n",
		"a bad unit":                "logs:\n  units: ['x y']\n",
		"a relative directory":      "actions:\n  x: {command: [/bin/true], dir: tmp}\n",
	} {
		if _, err := LoadConfig(writeConfig(t, text)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"nginx": true, "app@1.service": true, "my-app_2": true, "a:b": true, "0abc": true,
		"": false, "-x": false, ".hidden": false, "a b": false, "a/b": false, "a;b": false, "a\nb": false, "é": false, strings.Repeat("a", 129): false,
	} {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v", name, got)
		}
	}
}
