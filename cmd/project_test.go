package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dymmer-code/dym/internal/api"
)

// TestSecretsDotenvWritesOnlyBodyToStdout is the plan's own example test.
func TestSecretsDotenvWritesOnlyBodyToStdout(t *testing.T) {
	fake := &fakeAPI{secretsResult: &api.SecretsResult{Raw: "API_KEY=value\n"}}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{API: fake, Out: out, Err: errOut})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get", "--output", "dotenv"})
	if err := cmd.Execute(); err != nil || out.String() != "API_KEY=value\n" || errOut.Len() != 0 {
		t.Fatal(err, out.String(), errOut.String())
	}
}

func TestSecretsDefaultsEnvToDev(t *testing.T) {
	fake := &fakeAPI{}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{API: fake, Out: out, Err: errOut})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastSecretsEnv != "dev" {
		t.Fatalf("env = %q, want dev", fake.lastSecretsEnv)
	}
	if fake.lastSecretsProject != "demo" {
		t.Fatalf("project = %q, want demo", fake.lastSecretsProject)
	}
}

func TestSecretsEnvFlagPassesThrough(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{API: fake, Out: new(bytes.Buffer), Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get", "--env", "prod"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastSecretsEnv != "prod" {
		t.Fatalf("env = %q, want prod", fake.lastSecretsEnv)
	}
}

func TestSecretsDeploymentOmittedWhenUnset(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{API: fake, Out: new(bytes.Buffer), Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastSecretsDeployment != "" {
		t.Fatalf("deployment = %q, want empty", fake.lastSecretsDeployment)
	}
}

func TestSecretsDeploymentFlagPassesThrough(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{API: fake, Out: new(bytes.Buffer), Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get", "--deployment", "blue"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastSecretsDeployment != "blue" {
		t.Fatalf("deployment = %q, want blue", fake.lastSecretsDeployment)
	}
}

func TestSecretsOutputJSONEncodesEntriesWithNewline(t *testing.T) {
	fake := &fakeAPI{secretsResult: &api.SecretsResult{Entries: []api.SecretEntry{{Key: "A", Value: "1"}}}}
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{API: fake, Out: out, Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get", "--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastSecretsFormat != "" {
		t.Fatalf("wire format = %q, want empty string for json", fake.lastSecretsFormat)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("expected trailing newline, got %q", out.String())
	}
	var got []api.SecretEntry
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%q)", err, out.String())
	}
	if len(got) != 1 || got[0].Key != "A" {
		t.Fatalf("decoded entries = %+v", got)
	}
}

func TestSecretsDefaultOutputIsTableOfJSONModeEntries(t *testing.T) {
	fake := &fakeAPI{secretsResult: &api.SecretsResult{Entries: []api.SecretEntry{{Key: "A", Value: "1"}}}}
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{API: fake, Out: out, Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// Table default still fetches JSON-mode entries from GetSecrets (wire
	// value "").
	if fake.lastSecretsFormat != "" {
		t.Fatalf("wire format = %q, want empty string for table (JSON-mode entries)", fake.lastSecretsFormat)
	}
	if strings.HasPrefix(strings.TrimSpace(out.String()), "[") {
		t.Fatalf("expected table output by default, got JSON-looking output: %q", out.String())
	}
	if !strings.Contains(out.String(), "KEY") || !strings.Contains(out.String(), "VALUE") || !strings.Contains(out.String(), "A") {
		t.Fatalf("table output missing expected content: %q", out.String())
	}
}

func TestSecretsOutputDotenvTranslatesWireValue(t *testing.T) {
	fake := &fakeAPI{secretsResult: &api.SecretsResult{Raw: "A=1\n"}}
	cmd := NewRootCommand(Dependencies{API: fake, Out: new(bytes.Buffer), Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get", "--output", "dotenv"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastSecretsFormat != ".env" {
		t.Fatalf("wire format = %q, want .env", fake.lastSecretsFormat)
	}
}

func TestSecretsInvalidOutputErrorsWithoutCallingAPI(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{API: fake, Out: new(bytes.Buffer), Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get", "--output", "yaml"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for invalid --output")
	}
	if fake.lastSecretsProject != "" {
		t.Fatal("must not call GetSecrets with an invalid --output")
	}
}

func TestSecretsAPIErrorPropagatesViaWrapAuthErrorWithoutStdout(t *testing.T) {
	fake := &fakeAPI{secretsErr: &api.APIError{StatusCode: 401, Reason: "authentication failed"}}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{API: fake, Out: out, Err: errOut})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "dym auth login") || !strings.Contains(err.Error(), "DYMMER_TOKEN") {
		t.Fatalf("error = %q, want guidance to run dym auth login or set DYMMER_TOKEN", err.Error())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", out.String())
	}
}

func TestSecretsGenericErrorPropagatesWithoutStdout(t *testing.T) {
	fake := &fakeAPI{secretsErr: errors.New("boom")}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{API: fake, Out: out, Err: errOut})
	cmd.SetArgs([]string{"project", "demo", "secrets", "get"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error to propagate")
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", out.String())
	}
}
