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
  zone-txt-records:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/records"
    auth: bearer
    params: [domain]
    response:
      - template: "{{range .Body.records}}{{.id}}\n{{end}}"
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
	if len(md.Response) != 0 {
		t.Fatalf("expected 0 response templates for mail-domains, got %d", len(md.Response))
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
	if len(ztr.Response) != 1 || ztr.Response[0].Template == nil {
		t.Fatal("expected 1 compiled response template for zone-txt-records")
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
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "url is required") {
		t.Fatalf("expected a url-required skip message, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileInvalidMethodIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    method: FROBNICATE\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "method must be one of") {
		t.Fatalf("expected a method validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileInvalidAuthIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    auth: maybe\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, `auth must be "none" or "bearer"`) {
		t.Fatalf("expected an auth validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileTokenSetWithAuthNoneIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    auth: none\n    token: \"abc\"\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, `token is set but auth is "none"`) {
		t.Fatalf("expected a token/auth validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileOldResponsePathIsRejected(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  bad:
    url: "http://example.com"
    response_path: domains
`)
	load := loadExtensionsFile(path)
	if load.FileErr == nil {
		t.Fatal("expected FileErr for legacy response_path")
	}
	if !strings.Contains(load.FileErr.Error(), "response_path") {
		t.Fatalf("expected response_path in error message, got %v", load.FileErr)
	}
}

func TestLoadExtensionsFileOldResponseTemplateIsRejected(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  bad:
    url: "http://example.com"
    response_template: "{{.}}"
`)
	load := loadExtensionsFile(path)
	if load.FileErr == nil {
		t.Fatal("expected FileErr for legacy response_template")
	}
	if !strings.Contains(load.FileErr.Error(), "response_template") {
		t.Fatalf("expected response_template in error message, got %v", load.FileErr)
	}
}

func TestLoadExtensionsFileEmptyResponseListIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  bad:
    url: "http://example.com"
    response: []
`)
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "response list cannot be empty") {
		t.Fatalf("expected empty response list validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileEmptyTemplateInResponseIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  bad:
    url: "http://example.com"
    response:
      - template: "   "
`)
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "template is required") {
		t.Fatalf("expected template required validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileInvalidResponseTemplateSyntaxIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  bad:
    url: "http://example.com"
    response:
      - template: "{{.unclosed"
`)
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "invalid response template") {
		t.Fatalf("expected invalid response template validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileRequestTemplateWithGETIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  bad:\n    url: \"http://example.com\"\n    request_template: '{}'\n")
	load := loadExtensionsFile(path)
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "request_template is only valid when method is not GET") {
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
	if len(load.Skipped) != 1 || load.Skipped[0].Name != "bad" {
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

// --- Integration tests via NewRootCommand ---

func TestExtGetTableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/mail_servers/domains" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`["a.example.com","b.example.com"]`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    description: "domains"
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
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

func TestExtGetJSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`["a.example.com","b.example.com"]`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
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
		_, _ = w.Write([]byte(`[{"id":"1","type":"A"},{"id":"2","type":"TXT"}]`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  recs:
    url: "{{.BaseURL}}/records"
    auth: none
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
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  recs:
    url: "{{.BaseURL}}/records"
    auth: bearer
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
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{
		Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path,
		Store: panicOnTouchStore{t: t},
		// Deliberately airtight rather than leaving Env unset (which would
		// default to os.Getenv): if the code under test wrongly called
		// credentials.Resolve for an auth: none extension, this fails the
		// test regardless of whether the developer's shell happens to have
		// DYMMER_TOKEN exported.
		Env: func(string) string { t.Fatal("env must not be read for auth: none"); return "" },
	})
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
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  zone-txt-records:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/records"
    auth: none
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
		_, _ = w.Write([]byte(`[{"id":"1"}]`))
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

// Item 4 (fix round 1): --filter/--select must be validated before the HTTP
// call fires, not after — otherwise a typo'd --filter on a POST extension
// would let the (possibly mutating) request through before the user ever
// finds out about the typo. Uses a hit counter on the test server, which a
// pre-request-fired regression would increment.
func TestExtMalformedFilterIsRejectedBeforeThePOSTFires(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`[{"id":"1"}]`))
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
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "create-dkim-txt", "example.com", "sometoken", "--filter", "nonsense-no-equals-sign"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a malformed --filter")
	}
	if !strings.Contains(err.Error(), "invalid --filter") {
		t.Fatalf("expected a malformed-filter error, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("expected the POST to never fire, but the server was hit %d time(s)", hits)
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
    response:
      - template: |-
          {{range .Body.domains}}{{.}}
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

	// --select must not be a registered flag on a response template extension.
	out2 := new(bytes.Buffer)
	cmd2 := NewRootCommand(Dependencies{Out: out2, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd2.SetArgs([]string{"ext", "mail-domains-pretty", "--select", "domains"})
	if err := cmd2.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected an unknown flag error for --select, got %v", err)
	}
}

// Response templates must be able to combine data that only exists in the
// response body (Body) with data that only exists in the request (Args) —
// e.g. a domain name declared as a param and interpolated into the URL, but
// never echoed back by the server in the response body itself. This mirrors
// the CLI owner's concrete mailbox-passwd-lines use case.
func TestExtResponseTemplateCombinesArgsAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Deliberately has no "domain" field anywhere: the server never
		// echoes back a value that was only ever in the URL/params.
		_, _ = w.Write([]byte(`{"mailboxes":[{"username":"alice","enabled":true,"password_md5":"abc123"},{"username":"bob","enabled":false,"password_md5":"def456"}]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  mailbox-passwd-lines:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/mailboxes"
    auth: none
    params: [domain]
    response:
      - template: |-
          {{$domain := .Args.domain}}{{range .Body.mailboxes}}{{if .enabled}}{{.username}}@{{$domain}}:{{.password_md5}}{{"\n"}}{{end}}{{end}}
`)

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "mailbox-passwd-lines", "example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "alice@example.com:abc123\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestExtSingleTemplateWithToAndAppendTo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"msg":"hello"}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  single:
    url: "{{.BaseURL}}/x"
    auth: none
    response:
      - template: "{{.Body.msg}}\n"
`)

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.txt")

	// 1. Write with --to
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "single", "--to", outFile})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("got %q, want hello\\n", string(content))
	}

	// 2. Append with --append-to
	cmd = NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "single", "--append-to", outFile})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello\nhello\n" {
		t.Fatalf("got %q, want hello\\nhello\\n", string(content))
	}

	// 3. Overwrite with --to
	cmd = NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "single", "--to", outFile})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("got %q, want hello\\n (overwritten)", string(content))
	}

	// 4. Too many flags for single template -> error before request
	cmd = NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "single", "--to", outFile, "--append-to", outFile})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "declares 1 response template but got 2") {
		t.Fatalf("expected flag count error, got %v", err)
	}
}

func TestExtMultipleTemplatesRoutingAndFlagOrdering(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"domain":"example.com","users":["alice","bob"]}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  multi:
    url: "{{.BaseURL}}/x"
    auth: none
    response:
      - template: |-
          {{range .Body.users}}{{.}}@{{$.Body.domain}} OK
          {{end}}
      - template: |-
          {{range .Body.users}}{{.}}@{{$.Body.domain}} {{.}}
          {{end}}
`)

	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	// 1. Missing flags (0 flags for 2 templates) -> rejected before HTTP request
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "multi"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "declares 2 response templates but got 0") {
		t.Fatalf("expected error for 0 flags on 2 templates, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("expected 0 HTTP hits, got %d", hits)
	}

	// 2. 1 flag for 2 templates -> rejected before HTTP request
	cmd = NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "multi", "--to", file1})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "declares 2 response templates but got 1") {
		t.Fatalf("expected error for 1 flag on 2 templates, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("expected 0 HTTP hits, got %d", hits)
	}

	// 3. Exactly 2 flags: --to file1 --append-to file2
	cmd = NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "multi", "--to", file1, "--append-to", file2})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("expected 1 HTTP hit, got %d", hits)
	}

	c1, err := os.ReadFile(file1)
	if err != nil {
		t.Fatal(err)
	}
	want1 := "alice@example.com OK\nbob@example.com OK\n"
	if string(c1) != want1 {
		t.Fatalf("file1: got %q, want %q", string(c1), want1)
	}

	c2, err := os.ReadFile(file2)
	if err != nil {
		t.Fatal(err)
	}
	want2 := "alice@example.com alice\nbob@example.com bob\n"
	if string(c2) != want2 {
		t.Fatalf("file2: got %q, want %q", string(c2), want2)
	}

	// 4. Test append behavior on second execution
	cmd = NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "multi", "--to", file1, "--append-to", file2})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	// file1 was overwritten (--to)
	c1, _ = os.ReadFile(file1)
	if string(c1) != want1 {
		t.Fatalf("file1 after overwrite: got %q, want %q", string(c1), want1)
	}

	// file2 was appended (--append-to)
	c2, _ = os.ReadFile(file2)
	if string(c2) != want2+want2 {
		t.Fatalf("file2 after append: got %q, want %q", string(c2), want2+want2)
	}

	// 5. Reverse flag order: --append-to file2 --to file1 (order in args determines mapping!)
	cmd = NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "multi", "--append-to", file2, "--to", file1})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// Template 1 went to file2 (appended), Template 2 went to file1 (overwritten)
	c1, _ = os.ReadFile(file1)
	if string(c1) != want2 {
		t.Fatalf("file1 with template 2: got %q, want %q", string(c1), want2)
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

// --- Fix round 1 regression tests ---

// Item 1: a broken config file must not let "dym ext <name>" exit 0 with
// cobra's default help output instead of a real error.
func TestExtBrokenFileDoesNotSilentlySucceed(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  broken: [this is not valid yaml\n")
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: errOut, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "mail-domains"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a broken extensions file, got exit 0")
	}
	if !strings.Contains(err.Error(), "failed to load extensions file") {
		t.Fatalf("expected a clear load-failure error, got %v", err)
	}
	if strings.Contains(out.String(), "Available Commands") {
		t.Fatalf("must not fall back to help output, got stdout %q", out.String())
	}
}

// Item 1: "dym ext list" itself must also report the load error, not print
// the empty-extensions message.
func TestExtListWithBrokenFileReturnsError(t *testing.T) {
	path := writeExtensionsYAML(t, "extensions:\n  broken: [this is not valid yaml\n")
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "list"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "failed to load extensions file") {
		t.Fatalf("expected a load-failure error from list too, got %v", err)
	}
}

// Item 1: requesting an extension name that failed its own validation
// (but where the rest of the file is fine) must name the skip reason, not
// produce a generic "unknown command" error.
func TestExtRequestingSkippedExtensionNamesTheReason(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  good:
    url: "http://example.com/good"
    auth: none
  bad:
    auth: none
`)
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "bad"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error naming why 'bad' was skipped")
	}
	if !strings.Contains(err.Error(), "bad") || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("expected the skip reason in the error, got %v", err)
	}
}

// Item 2: the response body is never buffered past maxExtensionResponseBytes,
// on the success path (a distinct, clear truncation error) and the error
// path (the displayed snippet stays capped regardless of how big the real
// body was).
func TestExtLargeSuccessResponseIsBoundedNotBuffered(t *testing.T) {
	huge := bytes.Repeat([]byte("a"), maxExtensionResponseBytes+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(huge)
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  big:
    url: "{{.BaseURL}}/big"
    auth: none
`)
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "big"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for an oversized response")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected a clear truncation error, got %v", err)
	}
}

// Item 3: a bearer token embedded in the url template must never appear in
// an error message — only the redacted stand-in should.
func TestExtTokenNeverLeaksIntoConnectionFailureError(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  leaky:
    url: "http://127.0.0.1:1/x?t={{.DymmerToken}}"
    auth: bearer
`)
	store := &fakeStore{token: "SUPER-SECRET-SUFFIX"}
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), ExtensionsFile: path,
		Store: store, Env: func(string) string { return "" },
	})
	cmd.SetArgs([]string{"ext", "leaky"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a connection error against 127.0.0.1:1")
	}
	if strings.Contains(err.Error(), "SUPER-SECRET-SUFFIX") {
		t.Fatalf("token leaked into error message: %v", err)
	}
	if !strings.Contains(err.Error(), redactedTokenPlaceholder) {
		t.Fatalf("expected the redacted placeholder in the error, got %v", err)
	}
}

// Fix round 2, item A1: the plain string-replace in sanitizeExtensionError
// is defeated when the token sits in the URL *path* and contains a
// character Go's URL encoding percent-escapes (a space or "|", here) — a
// *url.Error's own .Error() string embeds req.URL.String(), which
// re-serializes (and thus re-percent-encodes) the URL independently of the
// raw, unescaped string our template rendered, so a literal string-replace
// against the raw string never matches the *encoded* form and the
// (trivially percent-decodable) token survives. safeRequestError must
// special-case *url.Error and drop its .URL field entirely rather than try
// to sanitize its re-encoded form.
func TestExtTokenSurvivingPercentEncodingIsStillRedacted(t *testing.T) {
	const token = "secret token|value" // space + "|": both get percent-escaped by (*url.URL).String()
	path := writeExtensionsYAML(t, `
extensions:
  leaky-path:
    url: "http://127.0.0.1:1/creds/{{.DymmerToken}}"
    auth: bearer
`)
	store := &fakeStore{token: token}
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), ExtensionsFile: path,
		Store: store, Env: func(string) string { return "" },
	})
	cmd.SetArgs([]string{"ext", "leaky-path"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a connection error against 127.0.0.1:1")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked (raw form) into error message: %v", err)
	}
	// The percent-encoded form ("%20"/"%7C" in place of the space/"|") is
	// exactly what a plain string-replace against the raw token would miss;
	// it must not appear either, since it round-trips right back to the
	// real token via url.QueryUnescape.
	if strings.Contains(err.Error(), "%20") || strings.Contains(err.Error(), "%7C") || strings.Contains(err.Error(), "%7c") {
		t.Fatalf("token leaked (percent-encoded form) into error message: %v", err)
	}
	if !strings.Contains(err.Error(), redactedTokenPlaceholder) {
		t.Fatalf("expected the redacted placeholder in the error, got %v", err)
	}
}

// Item 3: same guarantee on the non-2xx path, where the (real) URL would
// otherwise be echoed directly into the error message.
func TestExtTokenNeverLeaksIntoNon2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  leaky:
    url: "{{.BaseURL}}/x?t={{.DymmerToken}}"
    auth: bearer
`)
	store := &fakeStore{token: "SUPER-SECRET-SUFFIX"}
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path,
		Store: store, Env: func(string) string { return "" },
	})
	cmd.SetArgs([]string{"ext", "leaky"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a 404 error")
	}
	if strings.Contains(err.Error(), "SUPER-SECRET-SUFFIX") {
		t.Fatalf("token leaked into error message: %v", err)
	}
	if !strings.Contains(err.Error(), redactedTokenPlaceholder) {
		t.Fatalf("expected the redacted placeholder in the error, got %v", err)
	}
}

// Item 5: an extension named after a built-in dym ext subcommand (or
// containing whitespace) must be rejected at load time, not silently
// shadowed/misrouted.
func TestLoadExtensionsFileReservedNameIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  list:
    url: "http://example.com"
    auth: none
`)
	load := loadExtensionsFile(path)
	if len(load.Extensions) != 0 {
		t.Fatalf("expected 'list' to be rejected, got %v", load.Extensions)
	}
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "reserved") {
		t.Fatalf("expected a reserved-name validation error, got %v", load.Skipped)
	}
}

func TestLoadExtensionsFileWhitespaceNameIsValidationError(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  "foo bar":
    url: "http://example.com"
    auth: none
`)
	load := loadExtensionsFile(path)
	if len(load.Extensions) != 0 {
		t.Fatalf("expected 'foo bar' to be rejected, got %v", load.Extensions)
	}
	if len(load.Skipped) != 1 || !strings.Contains(load.Skipped[0].Reason, "whitespace") {
		t.Fatalf("expected a whitespace validation error, got %v", load.Skipped)
	}
}

// Item 6: splitExtensionsFileFlag unit coverage for the concrete bugs found
// in review: repeated-flag last-wins, and "--" unconditionally terminating
// the scan (so anything after it, including literal "--extensions-file"
// text, passes through untouched as ordinary positional data).
func TestSplitExtensionsFileFlagRepeatedFlagLastWins(t *testing.T) {
	value, remaining := splitExtensionsFileFlag([]string{"foo", "--extensions-file", "a", "--extensions-file", "b"})
	if value != "b" {
		t.Fatalf("got value %q, want %q", value, "b")
	}
	if len(remaining) != 1 || remaining[0] != "foo" {
		t.Fatalf("got remaining %v, want [foo]", remaining)
	}
}

func TestSplitExtensionsFileFlagDoubleDashTerminatesScanning(t *testing.T) {
	value, remaining := splitExtensionsFileFlag([]string{"foo", "--", "--extensions-file", "/evil.yaml"})
	if value != "" {
		t.Fatalf("expected no value extracted after --, got %q", value)
	}
	want := []string{"foo", "--", "--extensions-file", "/evil.yaml"}
	if len(remaining) != len(want) {
		t.Fatalf("got remaining %v, want %v", remaining, want)
	}
	for i := range want {
		if remaining[i] != want[i] {
			t.Fatalf("got remaining %v, want %v", remaining, want)
		}
	}
}

func TestSplitExtensionsFileFlagEqualsAndShortForms(t *testing.T) {
	cases := []struct {
		args      []string
		wantValue string
	}{
		{[]string{"--extensions-file=/a.yaml"}, "/a.yaml"},
		{[]string{"-e", "/b.yaml"}, "/b.yaml"},
		{[]string{"-e=/c.yaml"}, "/c.yaml"},
	}
	for _, c := range cases {
		value, _ := splitExtensionsFileFlag(c.args)
		if value != c.wantValue {
			t.Fatalf("args %v: got value %q, want %q", c.args, value, c.wantValue)
		}
	}
}

func TestSplitExtensionsFileFlagPreservesOtherFlagsAndPositionals(t *testing.T) {
	value, remaining := splitExtensionsFileFlag([]string{
		"zone-txt-records", "example.com", "--extensions-file", "/a.yaml", "--filter", "type=A", "--select", "id",
	})
	if value != "/a.yaml" {
		t.Fatalf("got value %q", value)
	}
	want := []string{"zone-txt-records", "example.com", "--filter", "type=A", "--select", "id"}
	if len(remaining) != len(want) {
		t.Fatalf("got remaining %v, want %v", remaining, want)
	}
	for i := range want {
		if remaining[i] != want[i] {
			t.Fatalf("got remaining %v, want %v", remaining, want)
		}
	}
}

// Item 6: the real --extensions-file flag, resolved through the actual
// cmd.SetArgs(...) path (not the Dependencies.ExtensionsFile test-only
// override), must work end to end.
func TestExtExtensionsFileFlagThroughRealArgs(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  mail-domains:
    description: "domains"
    url: "http://example.com/domains"
    auth: none
`)
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"ext", "--extensions-file", path, "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "mail-domains") {
		t.Fatalf("expected mail-domains listed, got %q", out.String())
	}
}

// Item 6 (hermeticity, strengthened per fix-round-2 review): building and
// running a completely unrelated command must not depend on any extensions
// file existing (or not) at the real, resolved default location — this is
// true by construction (newExtCommand does no file I/O outside its own
// RunE), and this test actually proves it rather than merely running
// against whatever (probably absent) file happens to be at the default
// location on the machine running the test: it plants a deliberately
// malformed extensions.yaml at the real os.UserConfigDir()-resolved default
// path (via t.Setenv on HOME/XDG_CONFIG_HOME, restored automatically) and
// confirms a credential-free command is completely unaffected. Against the
// old eager-load-at-NewRootCommand-time code, this would have failed (or at
// least logged/surfaced the parse error somewhere), since that code read
// this exact file during every NewRootCommand call regardless of which
// command was about to run.
func TestNonExtCommandDoesNotDependOnExtensionsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-config"))
	t.Setenv("AppData", filepath.Join(dir, "AppData"))

	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir(): %v", err)
	}
	extDir := filepath.Join(configDir, "dym")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "extensions.yaml"), []byte("extensions:\n  broken: [this is not valid yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"auth", "token-help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("auth token-help must be unaffected by a broken extensions.yaml at the default path, got: %v", err)
	}
}

// Item 7: a typo'd {{.Args.foo}} must fail loudly at render time rather
// than silently rendering "<no value>" and sending a bogus request.
func TestExtTypoedArgsFieldErrorsInsteadOfNoValue(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  typo:
    url: "{{.BaseURL}}/{{.Args.domian}}"
    auth: none
    params: [domain]
`)
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "typo", "example.com"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a typo'd {{.Args.domian}}")
	}
	if strings.Contains(err.Error(), "<no value>") {
		t.Fatalf("must not silently render <no value>, got %v", err)
	}
	if called {
		t.Fatal("must not have sent a request with the bogus rendered URL")
	}
}

// Item 8: a misspelled schema key must be a load-time error, not a
// silently-dropped typo.
func TestLoadExtensionsFileUnknownKeyIsRejected(t *testing.T) {
	path := writeExtensionsYAML(t, `
extensions:
  bad:
    url: "http://example.com"
    auth: none
    responsepath: domains
`)
	load := loadExtensionsFile(path)
	if load.FileErr == nil {
		t.Fatal("expected a FileErr for an unrecognized key")
	}
	if !strings.Contains(load.FileErr.Error(), "responsepath") {
		t.Fatalf("expected the bad key named in the error, got %v", load.FileErr)
	}
}

// Item 9: a 401 from a bearer-auth extension gets the same "run dym auth
// login" guidance every native command's wrapAuthError adds; an auth: none
// extension's 401 does not (there's no token to refresh).
func TestExt401BearerGetsAuthLoginGuidance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  needs-auth:
    url: "{{.BaseURL}}/x"
    auth: bearer
`)
	store := &fakeStore{token: "tok"}
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path,
		Store: store, Env: func(string) string { return "" },
	})
	cmd.SetArgs([]string{"ext", "needs-auth"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `dym auth login`) {
		t.Fatalf("expected auth-login guidance, got %v", err)
	}
}

func TestExt401AuthNoneGetsNoAuthLoginGuidance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  open:
    url: "{{.BaseURL}}/x"
    auth: none
`)
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "open"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a 401 error")
	}
	if strings.Contains(err.Error(), "dym auth login") {
		t.Fatalf("auth: none extension should not get login guidance, got %v", err)
	}
}

// Item 10: error when response body is not a JSON array in default tabular mode.
func TestExtArrayTypeErrorMessageVariants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"not":"an array"}`))
	}))
	defer srv.Close()

	path := writeExtensionsYAML(t, `
extensions:
  noPath:
    url: "{{.BaseURL}}/x"
    auth: none
`)
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), BaseURL: srv.URL, ExtensionsFile: path})
	cmd.SetArgs([]string{"ext", "noPath"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "expected the response body to be a JSON array") {
		t.Fatalf("unexpected message for the no-path case: %v", err)
	}
}
