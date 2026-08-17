package cmd

import (
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
// verbatim before per-extension validation/normalization.
type rawExtensionsFile struct {
	Extensions map[string]rawExtension `yaml:"extensions"`
}

// rawExtension mirrors the extensions.yaml schema for a single extension,
// field for field, before any defaulting/validation is applied.
type rawExtension struct {
	Description      string   `yaml:"description"`
	URL              string   `yaml:"url"`
	Method           string   `yaml:"method"`
	Auth             string   `yaml:"auth"`
	Token            string   `yaml:"token"`
	ResponsePath     string   `yaml:"response_path"`
	Params           []string `yaml:"params"`
	RequestTemplate  string   `yaml:"request_template"`
	ResponseTemplate string   `yaml:"response_template"`
}

// extension is a validated, normalized extension definition: method and auth
// are uppercased/lowercased respectively, and every Go template field has
// already been parsed (so a syntax error surfaces at load time, not on first
// use).
type extension struct {
	Name             string
	Description      string
	URLTemplate      *template.Template
	Method           string             // "GET", "POST", "PUT", "PATCH", or "DELETE"
	Auth             string             // "none" or "bearer"
	TokenTemplate    *template.Template // nil unless Auth == "bearer"
	ResponsePath     string
	Params           []string
	RequestTemplate  *template.Template // nil when unset
	ResponseTemplate *template.Template // nil when unset
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
	Skipped    []string // "name: reason", sorted, for extensions that failed validation
}

// loadExtensionsFile reads and validates the extensions file at path. A
// nonexistent file is reported via Missing, not FileErr — "no extensions
// configured yet" is a normal, expected state, not an error. A file that
// exists but fails to parse as YAML is reported via FileErr with zero
// extensions loaded. Once the file itself parses, each extension is
// validated independently: a validation failure on one extension is
// recorded in Skipped and that extension is excluded, but does not stop the
// rest of the file from loading.
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
	var raw rawExtensionsFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		result.FileErr = fmt.Errorf("parsing extensions file %s: %w", path, err)
		return result
	}
	for name, def := range raw.Extensions {
		ext, err := buildExtension(name, def)
		if err != nil {
			result.Skipped = append(result.Skipped, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		result.Extensions[name] = ext
	}
	sort.Strings(result.Skipped)
	return result
}

// buildExtension validates and normalizes a single raw extension definition,
// parsing every template field so a syntax error is caught at load time.
func buildExtension(name string, raw rawExtension) (*extension, error) {
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

	if raw.ResponsePath != "" && raw.ResponseTemplate != "" {
		return nil, fmt.Errorf("response_path and response_template are mutually exclusive")
	}

	if raw.RequestTemplate != "" && method == "GET" {
		return nil, fmt.Errorf("request_template is only valid when method is not GET")
	}

	urlTmpl, err := template.New(name + ":url").Parse(raw.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid url template: %w", err)
	}

	var tokenTmpl *template.Template
	if auth == "bearer" {
		token := raw.Token
		if token == "" {
			token = "{{.DymmerToken}}"
		}
		tokenTmpl, err = template.New(name + ":token").Parse(token)
		if err != nil {
			return nil, fmt.Errorf("invalid token template: %w", err)
		}
	}

	var requestTmpl *template.Template
	if raw.RequestTemplate != "" {
		requestTmpl, err = template.New(name + ":request").Parse(raw.RequestTemplate)
		if err != nil {
			return nil, fmt.Errorf("invalid request_template: %w", err)
		}
	}

	var responseTmpl *template.Template
	if raw.ResponseTemplate != "" {
		responseTmpl, err = template.New(name + ":response").Parse(raw.ResponseTemplate)
		if err != nil {
			return nil, fmt.Errorf("invalid response_template: %w", err)
		}
	}

	return &extension{
		Name:             name,
		Description:      raw.Description,
		URLTemplate:      urlTmpl,
		Method:           method,
		Auth:             auth,
		TokenTemplate:    tokenTmpl,
		ResponsePath:     raw.ResponsePath,
		Params:           append([]string(nil), raw.Params...),
		RequestTemplate:  requestTmpl,
		ResponseTemplate: responseTmpl,
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
// array are wrapped as {"value": ...} (see the response_path handling in
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

// scanArgsForExtensionsFile looks for a "--extensions-file <path>" or
// "--extensions-file=<path>" pair in args, returning the value and true if
// found. This is used instead of ordinary cobra flag parsing because the
// extensions file must be loaded (to know which extension subcommands and
// flags to register) before the command tree — and therefore before cobra's
// own flag parsing — exists at all.
func scanArgsForExtensionsFile(args []string) (string, bool) {
	const flag = "--extensions-file"
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v, true
		}
	}
	return "", false
}

// resolveExtensionsFilePath resolves the extensions.yaml path to use, in
// order: deps.ExtensionsFile (test-only override, used directly with no
// further fallback) > the "--extensions-file" flag on the real process
// arguments > the DYM_EXTENSIONS_FILE environment variable (via deps.Env) >
// the default location, os.UserConfigDir()/dym/extensions.yaml.
func resolveExtensionsFilePath(deps Dependencies) string {
	if deps.ExtensionsFile != "" {
		return deps.ExtensionsFile
	}
	if v, ok := scanArgsForExtensionsFile(os.Args[1:]); ok && v != "" {
		return v
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
