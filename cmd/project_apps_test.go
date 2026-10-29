package cmd

import (
	"bytes"
	"github.com/dymmer-code/dym/internal/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectAppsListsDestinations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hosts/deployments" || r.URL.Query().Get("project") != "My Project" || r.URL.Query().Get("env") != "prod" || r.URL.Query().Get("deployment") != "API" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"app","name":"API","hosts":[{"id":"a","host_id":"one","name":"rowan","status":"ok","ports":[4000]},{"id":"b","host_id":"two","name":"aspen","status":"no","ports":[5000]}]}]`))
	}))
	defer server.Close()
	for _, output := range []string{"table", "json", "csv", "tsv"} {
		out := new(bytes.Buffer)
		cmd := NewRootCommand(Dependencies{API: api.NewClient(server.URL, "token", server.Client()), Out: out, Err: new(bytes.Buffer)})
		cmd.SetArgs([]string{"project", "My Project", "apps", "list", "--env", "prod", "--deployment", "API", "--output", output})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"API", "rowan", "aspen", "4000", "5000"} {
			if !strings.Contains(out.String(), value) {
				t.Errorf("%s output missing %s: %s", output, value, out.String())
			}
		}
	}
}

func TestProjectAppsRejectsInvalidOutput(t *testing.T) {
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "apps", "list", "--output", "dotenv"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected invalid format error")
	}
}
