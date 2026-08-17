package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeExtensionsYAML writes contents to a fresh extensions.yaml under a
// t.TempDir() and returns its path.
func writeExtensionsYAML(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "extensions.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- YAML loading/validation ---

func TestLoadExtensionsFileValidLoadsCorrectly(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    description: "Authorized mail-server domains"
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
    response_path: domains
  zone-txt-records:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/records"
    auth: bearer
    response_path: records
    params: [domain]
`)
	load := loadExtensionsFile(path)
	if load.FileErr != nil {
		t.Fatalf("unexpected FileErr: %v", load.FileErr)
	}
	if load.Missing {
		t.Fatal("expected Missing to be false")
	}
	if len(load.Skipped) != 0 {
		t.Fatalf("expected no skipped extensions, got %v", load.Skipped)
	}
	if len(load.Extensions) != 2 {
		t.Fatalf("expected 2 extensions, got %d", len(load.Extensions))
	}
	md := load.Extensions["mail-domains"]
	if md == nil {
		t.Fatal("mail-domains not loaded")
	}
	if md.Method != "GET" {
		t.Fatalf("expected default method GET, got %q", md.Method)
	}
	if md.Auth != "none" {
		t.Fatalf("expected auth none, got %q", md.Auth)
	}
	ztr := load.Extensions["zone-txt-records"]
	if ztr == nil {
		t.Fatal("zone-txt-records not loaded")
	}
	if len(ztr.Params) != 1 || ztr.Params[0] != "domain" {
		t.Fatalf("expected params [domain], got %v", ztr.Params)
	}
	if ztr.TokenTemplate == nil {
		t.Fatal("expected a default token template for auth: bearer")
	}
}

func TestLoadExtensionsFileMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.yaml")
	load := loadExtensionsFile(path)
	if !load.Missing {
		t.Fatal("expected Missing to be true")
	}
	if load.FileErr != nil {
		t.Fatalf("expected no FileErr for a missing file, got %v", load.FileErr)
	}
	if len(load.Extensions) != 0 {
		t.Fatalf("expected zero extensions, got %d", len(load.Extensions))
	}
}

func TestLoadExtensionsFileMalformedYAMLReportsFileErr(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  broken: [this is not valid yaml\n")
	load := loadExtensionsFile(path)
	if load.FileErr == nil {
		t.Fatal("expected FileErr for malformed YAML")
	}
	if len(load.Extensions) != 0 {
		t.Fatalf("expected zero extensions, got %d", len(load.Extensions))
	}
}

func TestLoadExtensionsFileMissingURLIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    auth: none\n")
	load := loadExtensionsFile(path)
	if len(load.Extensions) != 0 {
		t.Fatalf("expected bad extension to be skipped, got %v", load.Extensions)
	}
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0], "url is required") {
		t.Fatalf("expected a url-required skip message, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileInvalidMethodIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    method: FROBNICATE\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0], "method must be one of") {
		t.Fatalf("expected a method validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileInvalidAuthIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    auth: maybe\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0], `auth must be "none" or "bearer"`) {
		t.Fatalf("expected an auth validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileTokenSetWithAuthNoneIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    auth: none\n    token: \"abc\"\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0], `token is set but auth is "none"`) {
		t.Fatalf("expected a token/auth validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileResponsePathAndTemplateMutuallyExclusive(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    response_path: domains\n    response_template: \"{{.}}\"\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0], "mutually exclusive") {
		t.Fatalf("expected a mutual-exclusion validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileRequestTemplateWithGETIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    request_template: '{}'\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0], "request_template is only valid when method is not GET") {
		t.Fatalf("expected a request_template/method validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileOneBrokenExtensionDoesNotBlockValidOnes(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  good:
    url: "http://example.com/good"
    auth: none
  bad:
    auth: none
`)
	load := loadExtensionsFile(path)
	if len(load.Extensions) != 1 || load.Extensions["good"] == nil {
		t.Fatalf("expected only 'good' to load, got %v", load.Extensions)
	}
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0], "bad:") {
		t.Fatalf("expected 'bad' to be reported skipped, got %v", load.Skipped)
	}
}

// --- defaultFieldsFromRows ---

func TestDefaultFieldsFromRowsUnionSortedAcrossHeterogeneousRows(t *testing.T) {
	rows := []map[string]any{
		{"id": "1", "name": "a"},
		{"id": "2", "extra": "x"},
	}
	fields := defaultFieldsFromRows(rows)
	want := []string{"extra", "id", "name"}
	if len(fields) != len(want) {
		t.Fatalf("got %v, want %v", fields, want)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("got %v, want %v", fields, want)
		}
	}
}

func TestDefaultFieldsFromRowsScalarWrappedValue(t *testing.T) {
	rows := []map[string]any{
		{"value": "a.com"},
		{"value": "b.com"},
	}
	fields := defaultFieldsFromRows(rows)
	if len(fields) != 1 || fields[0] != "value" {
		t.Fatalf("got %v, want [value]", fields)
	}
}

func TestDefaultFieldsFromRowsEmpty(t *testing.T) {
	fields := defaultFieldsFromRows(nil)
	if len(fields) != 0 {
		t.Fatalf("got %v, want empty", fields)
	}
}

// --- response_path resolution against a nested response ---

func TestResponsePathResolutionMailServersDomainsShape(t *testing.T) {
	var decoded any
	body := `{"status":"ok","domains":["a.example.com","b.example.com"]}`
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		t.Fatal("expected a JSON object")
	}
	v, ok := getPath(m, "domains")
	if !ok {
		t.Fatal("expected domains to resolve")
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("expected a 2-element array, got %#v", v)
	}
	if arr[0] != "a.example.com" || arr[1] != "b.example.com" {
		t.Fatalf("unexpected array contents: %v", arr)
	}
}

// --- Integration tests via NewRootCommand ---

func TestExtGetWithResponsePathTableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/mail_servers/domains" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok","domains":["a.example.com","b.example.com"]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    description: "domains"
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
    response_path: domains
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "mail-domains"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "VALUE") || !strings.Contains(got, "a.example.com") || !strings.Contains(got, "b.example.com") {
		t.Fatalf("unexpected table output: %q", got)
	}
}

func TestExtGetWithResponsePathJSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"domains":["a.example.com","b.example.com"]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
    response_path: domains
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "mail-domains", "--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := `[{"value":"a.example.com"},{"value":"b.example.com"}]` + "\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestExtGetWithSelectAndFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"records":[{"id":"1","type":"A"},{"id":"2","type":"TXT"}]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  recs:
    url: "{{.BaseURL}}/records"
    auth: none
    response_path: records
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "recs", "--select", "id", "--filter", "type=TXT"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "2") || strings.Contains(got, "1\n") {
		t.Fatalf("expected only the TXT (id=2) row, got %q", got)
	}
}

func TestExtBearerAuthSendsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"records":[]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  recs:
    url: "{{.BaseURL}}/records"
    auth: bearer
    response_path: records
`)

	out := new(bytes.Buffer)
	store := &fakeStore{token: "keychain-token"}
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path, Store: store, Env: func(string) string { return "" }})
	cmd.SetArgs([]string{"ext", "recs"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer keychain-token" {
		t.Fatalf("got Authorization %q", gotAuth)
	}
}

func TestExtAuthNoneSendsNoAuthorizationHeaderAndNeverTouchesStore(t *testing.T) {
	var gotAuth string
	var sawAuthHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, sawAuthHeader = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
		_, _ = w.Write([]byte(`{"domains":[]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
    response_path: domains
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path, Store: panicOnTouchStore{t: t}})
	cmd.SetArgs([]string{"ext", "mail-domains"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if sawAuthHeader {
		t.Fatalf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestExtParamsInterpolatedIntoURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"records":[]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  zone-txt-records:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/records"
    auth: none
    response_path: records
    params: [domain]
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "zone-txt-records", "example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/zones/example.com/records" {
		t.Fatalf("got path %q", gotPath)
	}
}

func TestExtPOSTRequestTemplateSendsRenderedJSONBody(t *testing.T) {
	var gotBody, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		gotBody = body.String()
		_, _ = w.Write([]byte(`{"records":[{"id":"1"}]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  create-dkim-txt:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/records"
    method: POST
    auth: none
    params: [domain, value]
    request_template: |
      {"record":{"host":"mail._domainkey","type":"TXT","content_value":"{{.Args.value}}"}}
    response_path: records
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "create-dkim-txt", "example.com", "sometoken"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotContentType != "application/json" {
		t.Fatalf("got Content-Type %q", gotContentType)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(gotBody), &parsed); err != nil {
		t.Fatalf("body was not valid JSON: %q", gotBody)
	}
	record, ok := parsed["record"].(map[string]any)
	if !ok || record["content_value"] != "sometoken" {
		t.Fatalf("unexpected rendered body: %q", gotBody)
	}
}

func TestExtRequestTemplateInvalidJSONErrorsClearly(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  bad-body:
    url: "http://example.invalid/records"
    method: POST
    auth: none
    params: [value]
    request_template: "not json: {{.Args.value}}"
`)
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "bad-body", "hello"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a request_template rendering invalid JSON")
	}
	if !strings.Contains(err.Error(), "did not render valid JSON") {
		t.Fatalf("expected a clear JSON-validity error, got %v", err)
	}
}

func TestExtResponseTemplateRendersVerbatimAndHasNoFilterSelectOutputFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"domains":["a.example.com","b.example.com"]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains-pretty:
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
    response_template: |-
      {{range .domains}}{{.}}
      {{end}}
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "mail-domains-pretty"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "a.example.com\nb.example.com\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}

	// --select must not be a registered flag on a response_template extension.
	out2 := new(bytes.Buffer)
	cmd2 := NewRootCommand(Dependencies{Out: out2, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd2.SetArgs([]string{"ext", "mail-domains-pretty", "--select", "domains"})
	if err := cmd2.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected an unknown flag error for --select, got %v", err)
	}
}

func TestExtListNoExtensions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "extensions.yaml")
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No extensions defined.") || !strings.Contains(out.String(), path) {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestExtListOneExtension(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    description: "Authorized mail-server domains"
    url: "http://example.com/domains"
    auth: none
`)
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "mail-domains") || !strings.Contains(got, "Authorized mail-server domains") {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestExtListMultipleExtensionsSortedByName(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  zzz-last:
    url: "http://example.com/z"
    auth: none
  aaa-first:
    url: "http://example.com/a"
    auth: none
`)
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Index(got, "aaa-first") > strings.Index(got, "zzz-last") {
		t.Fatalf("expected aaa-first before zzz-last, got %q", got)
	}
}

func TestExtNon2xxResponseIsClearError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
    response_path: domains
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "mail-domains"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected the status code in the error, got %v", err)
	}
}
