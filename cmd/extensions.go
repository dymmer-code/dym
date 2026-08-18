package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// rawExtensionsFile is the shape of a user-authored extensions.yaml, decoded
// verbatim before per-extension validation/normalization. Decoding uses
// strict (KnownFields) mode, so an unrecognized or removed key (e.g.
// "response_path" or "response_template") is a load-time error rather than
// a silently-dropped typo.
type rawExtensionsFile struct {
	Extensions map[string]rawExtension `yaml:"extensions"`
}

// rawResponseEntry is a single response output definition in extensions.yaml.
type rawResponseEntry struct {
	Template string `yaml:"template"`
}

// rawExtension mirrors the extensions.yaml schema for a single extension,
// field for field, before any defaulting/validation is applied.
type rawExtension struct {
	Description     string             `yaml:"description"`
	URL             string             `yaml:"url"`
	Method          string             `yaml:"method"`
	Auth            string             `yaml:"auth"`
	Token           string             `yaml:"token"`
	Params          []string           `yaml:"params"`
	RequestTemplate string             `yaml:"request_template"`
	Response        []rawResponseEntry `yaml:"response"`
}

// responseEntry is a validated, compiled template entry for response rendering.
type responseEntry struct {
	Template *template.Template
}

// extension is a validated, normalized extension definition: method and auth
// are uppercased/lowercased respectively, and every Go template field has
// already been parsed (so a syntax error surfaces at load time, not on first
// use). The url/token/request_template templates are parsed with
// "missingkey=error" so a typo'd {{.Args.foo}} fails loudly at render time
// instead of silently rendering the literal string "<no value>" and sending
// a bogus request. Response templates are parsed with the default (lenient)
// behavior instead, since they execute against arbitrary decoded JSON where a
// field being absent on a given response is often legitimate (e.g. an
// {{if .foo}} guard).
type extension struct {
	Name            string
	Description     string
	URLTemplate     *template.Template
	Method          string             // "GET", "POST", "PUT", "PATCH", or "DELETE"
	Auth            string             // "none" or "bearer"
	TokenTemplate   *template.Template // nil unless Auth == "bearer"
	Params          []string
	RequestTemplate *template.Template // nil when unset
	Response        []responseEntry    // empty when unset
}

// requestContext is the template data extension url/token/request_template
// templates render against. DymmerToken is only populated when the
// extension's auth is "bearer" — extensions with auth: none never resolve
// credentials at all.
type requestContext struct {
	BaseURL     string
	DymmerToken string
	Args        map[string]string
}

// responseTemplateContext is the template data response templates render
// against. Body is the raw decoded JSON response, and Args carries exactly the
// same {paramName: value} map the request-side templates see — so a response
// template can combine data that only exists in the response body with data that
// only exists in the request (e.g. a domain name declared as a param and
// interpolated into the URL, but never echoed back by the server in the
// response body itself). A template that writes "{{range .Body.domains}}" can
// additionally reference "{{.Args.domain}}".
type responseTemplateContext struct {
	Args map[string]string
	Body any
}

// buildArgsMap builds the {paramName: value} map from an extension's
// declared params and a subcommand invocation's positional args, in
// declaration order. Shared by doExtensionRequest (which uses it to build
// requestContext.Args for url/token/request_template) and
// newExtensionCommand's response templates branch (which uses it to build
// responseTemplateContext.Args) so both see identical values for the same
// invocation.
func buildArgsMap(ext *extension, args []string) map[string]string {
	m := make(map[string]string, len(ext.Params))
	for i, p := range ext.Params {
		m[p] = args[i]
	}
	return m
}

// skippedExtension records why one extension in an otherwise-loadable file
// failed validation and was excluded.
type skippedExtension struct {
	Name   string
	Reason string
}

// extensionsLoad is the result of loading and validating an extensions.yaml
// file. A missing file and a broken file are distinguished (Missing is not
// an error condition; FileErr is), and a broken individual extension does
// not prevent other, valid extensions in the same file from loading: it is
// recorded in Skipped and simply excluded from Extensions.
type extensionsLoad struct {
	Path       string
	Missing    bool
	FileErr    error
	Extensions map[string]*extension
	Skipped    []skippedExtension // sorted by Name
}

// skippedReason reports why name was skipped, if it was.
func (l extensionsLoad) skippedReason(name string) (string, bool) {
	for _, s := range l.Skipped {
		if s.Name == name {
			return s.Reason, true
		}
	}
	return "", false
}

// loadExtensionsFile reads and validates the extensions file at path. A
// nonexistent file is reported via Missing, not FileErr — "no extensions
// configured yet" is a normal, expected state, not an error. A file that
// exists but fails to parse as YAML (including an unrecognized key, since
// decoding is strict) is reported via FileErr with zero extensions loaded.
// An existing-but-empty file is treated the same as a missing one (zero
// extensions, no error) rather than an EOF decode error. Once the file
// itself parses, each extension is validated independently: a validation
// failure on one extension is recorded in Skipped and that extension is
// excluded, but does not stop the rest of the file from loading.
func loadExtensionsFile(path string) extensionsLoad {
	result := extensionsLoad{Path: path, Extensions: map[string]*extension{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.Missing = true
			return result
		}
		result.FileErr = fmt.Errorf("reading extensions file %s: %w", path, err)
		return result
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return result
	}
	var raw rawExtensionsFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		result.FileErr = fmt.Errorf("parsing extensions file %s: %w", path, err)
		return result
	}
	for name, def := range raw.Extensions {
		ext, err := buildExtension(name, def)
		if err != nil {
			result.Skipped = append(result.Skipped, skippedExtension{Name: name, Reason: err.Error()})
			continue
		}
		result.Extensions[name] = ext
	}
	sort.Slice(result.Skipped, func(i, j int) bool { return result.Skipped[i].Name < result.Skipped[j].Name })
	return result
}

// reservedExtensionNames are subcommand names dym ext always registers
// itself (or that cobra registers automatically), so a same-named extension
// would be permanently unreachable — dym ext <name> would resolve to the
// builtin, never the extension.
var reservedExtensionNames = map[string]bool{
	"list":       true,
	"help":       true,
	"completion": true,
}

// buildExtension validates and normalizes a single raw extension definition,
// parsing every template field so a syntax error is caught at load time.
func buildExtension(name string, raw rawExtension) (*extension, error) {
	if reservedExtensionNames[name] {
		return nil, fmt.Errorf("extension name %q is reserved (shadowed by a built-in dym ext subcommand) and would never be reachable", name)
	}
	if strings.ContainsAny(name, " \t\n\r\v\f") {
		return nil, fmt.Errorf("extension name %q contains whitespace, which cobra cannot route as a single subcommand", name)
	}

	if strings.TrimSpace(raw.URL) == "" {
		return nil, fmt.Errorf("url is required")
	}

	method := "GET"
	if raw.Method != "" {
		method = strings.ToUpper(raw.Method)
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return nil, fmt.Errorf("method must be one of GET, POST, PUT, PATCH, DELETE, got %q", raw.Method)
	}

	auth := "none"
	if raw.Auth != "" {
		auth = strings.ToLower(raw.Auth)
	}
	switch auth {
	case "none", "bearer":
	default:
		return nil, fmt.Errorf(`auth must be "none" or "bearer", got %q`, raw.Auth)
	}

	if auth == "none" && raw.Token != "" {
		return nil, fmt.Errorf(`token is set but auth is "none"`)
	}

	if raw.RequestTemplate != "" && method == "GET" {
		return nil, fmt.Errorf("request_template is only valid when method is not GET")
	}

	// url/token/request_template use missingkey=error: Args is a
	// map[string]string, and text/template's default "missingkey=default"
	// behavior silently renders an unset key as the literal string
	// "<no value>" instead of failing — which would send a bogus request
	// (e.g. to a URL literally containing "<no value>") on exit 0 for a
	// simple typo like {{.Args.domian}}. A typo'd struct field like
	// {{.Nope}} already errors on its own; this closes the same hole for
	// map lookups.
	urlTmpl, err := template.New(name + ":url").Option("missingkey=error").Parse(raw.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid url template: %w", err)
	}

	var tokenTmpl *template.Template
	if auth == "bearer" {
		token := raw.Token
		if token == "" {
			token = "{{.DymmerToken}}"
		}
		tokenTmpl, err = template.New(name + ":token").Option("missingkey=error").Parse(token)
		if err != nil {
			return nil, fmt.Errorf("invalid token template: %w", err)
		}
	}

	var requestTmpl *template.Template
	if raw.RequestTemplate != "" {
		requestTmpl, err = template.New(name + ":request").Option("missingkey=error").Parse(raw.RequestTemplate)
		if err != nil {
			return nil, fmt.Errorf("invalid request_template: %w", err)
		}
	}

	var responseEntries []responseEntry
	if raw.Response != nil {
		if len(raw.Response) == 0 {
			return nil, fmt.Errorf("response list cannot be empty")
		}
		responseEntries = make([]responseEntry, len(raw.Response))
		for i, r := range raw.Response {
			if strings.TrimSpace(r.Template) == "" {
				return nil, fmt.Errorf("response[%d]: template is required", i)
			}
			tmpl, err := template.New(fmt.Sprintf("%s:response:%d", name, i)).Parse(r.Template)
			if err != nil {
				return nil, fmt.Errorf("invalid response template [%d]: %w", i, err)
			}
			responseEntries[i] = responseEntry{Template: tmpl}
		}
	}

	return &extension{
		Name:            name,
		Description:     raw.Description,
		URLTemplate:     urlTmpl,
		Method:          method,
		Auth:            auth,
		TokenTemplate:   tokenTmpl,
		Params:          append([]string(nil), raw.Params...),
		RequestTemplate: requestTmpl,
		Response:        responseEntries,
	}, nil
}

// renderTemplate is a small convenience wrapper executing tmpl against data
// and returning the rendered output as a string.
func renderTemplate(tmpl *template.Template, data any) (string, error) {
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// defaultFieldsFromRows computes the sorted union of all top-level keys
// across rows. Extensions have no fixed per-type column set the way
// api.Record/api.Mailbox/api.Forwarding do (a row's shape is whatever the
// arbitrary configured endpoint returns), so when --select isn't given this
// is used as the default field list instead. Rows built from a scalar JSON
// array are wrapped as {"value": ...} (see writeExtensionRows in
// ext.go), so they contribute just the single key "value" to the union.
func defaultFieldsFromRows(rows []map[string]any) []string {
	set := map[string]struct{}{}
	for _, row := range rows {
		for k := range row {
			set[k] = struct{}{}
		}
	}
	fields := make([]string, 0, len(set))
	for k := range set {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields
}

// resolveExtensionsFilePath resolves the extensions.yaml path to use, in
// order: deps.ExtensionsFile (test-only override — set directly by tests,
// bypassing flag/env/default resolution entirely, and taking priority even
// over an explicit --extensions-file so tests never depend on real argv or
// environment state) > flagValue (the --extensions-file/-e flag, already
// extracted from the real command args by the caller — see
// splitExtensionsFileFlag in ext.go) > the DYM_EXTENSIONS_FILE environment
// variable (via deps.Env) > the default location,
// os.UserConfigDir()/dym/extensions.yaml.
func resolveExtensionsFilePath(deps Dependencies, flagValue string) string {
	if deps.ExtensionsFile != "" {
		return deps.ExtensionsFile
	}
	if flagValue != "" {
		return flagValue
	}
	if deps.Env != nil {
		if v := deps.Env("DYM_EXTENSIONS_FILE"); v != "" {
			return v
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join("dym", "extensions.yaml")
	}
	return filepath.Join(dir, "dym", "extensions.yaml")
}
