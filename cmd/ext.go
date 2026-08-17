package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/dymmer-code/dym/internal/credentials"
	"github.com/spf13/cobra"
)

// newExtCommand builds the "dym ext" parent command: a user-defined escape
// hatch for arbitrary HTTP calls declared in an extensions.yaml file (see
// cmd/extensions.go for the schema and loading/validation). Unlike
// credential resolution, this file is read once, eagerly, right here during
// tree construction — never inside a RunE — because cobra needs to know at
// tree-build time which subcommands and flags (--filter/--select/--output)
// to register per extension. This is unrelated to the "credential
// resolution stays lazy" invariant: it never touches the keychain or
// resolves a bearer token, only local file I/O.
func newExtCommand(deps Dependencies) *cobra.Command {
	path := resolveExtensionsFilePath(deps)
	load := loadExtensionsFile(path)

	cmd := &cobra.Command{Use: "ext", Short: "Run user-defined HTTP extensions (see extensions.yaml)"}

	// Registered for discoverability/--help only: by the time cobra would
	// parse it, the extensions file has already been resolved and loaded
	// (see resolveExtensionsFilePath, which scans the real process
	// arguments directly for this same flag ahead of tree construction).
	var extensionsFileFlag string
	cmd.PersistentFlags().StringVar(&extensionsFileFlag, "extensions-file", path,
		"Path to the extensions YAML file (default: OS config dir/dym/extensions.yaml, or $DYM_EXTENSIONS_FILE)")

	cmd.AddCommand(newExtListCommand(path, load))

	names := make([]string, 0, len(load.Extensions))
	for name := range load.Extensions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cmd.AddCommand(newExtensionCommand(deps, load.Extensions[name]))
	}
	return cmd
}

// newExtListCommand builds "dym ext list". A missing extensions file, or a
// file that parses but declares zero (usable) extensions, is reported as
// plain informational output on stdout with a zero exit code — "you haven't
// set any extensions up" is a normal state, not an error. A file that fails
// to parse at all is a genuine config problem and is reported as an error.
func newExtListCommand(path string, load extensionsLoad) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available extensions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if load.FileErr != nil {
				return fmt.Errorf("failed to load extensions file %s: %w", path, load.FileErr)
			}
			if load.Missing || (len(load.Extensions) == 0 && len(load.Skipped) == 0) {
				fmt.Fprintf(cmd.OutOrStdout(), "No extensions defined. Create one at %s.\n", path)
				return nil
			}
			out := cmd.OutOrStdout()
			if len(load.Extensions) > 0 {
				names := make([]string, 0, len(load.Extensions))
				for name := range load.Extensions {
					names = append(names, name)
				}
				sort.Strings(names)
				tw := newTabWriter(out)
				fmt.Fprintln(tw, "NAME\tDESCRIPTION")
				for _, name := range names {
					fmt.Fprintf(tw, "%s\t%s\n", name, load.Extensions[name].Description)
				}
				if err := tw.Flush(); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(out, "No usable extensions in %s.\n", path)
			}
			if len(load.Skipped) > 0 {
				fmt.Fprintf(out, "\n%d extension(s) in %s failed to load and were skipped:\n", len(load.Skipped), path)
				for _, s := range load.Skipped {
					fmt.Fprintln(out, "  "+s)
				}
			}
			return nil
		},
	}
}

// newExtensionCommand builds the generated subcommand for a single validated
// extension. When the extension has a response_template, --filter/--select/
// --output are not registered at all: the template fully owns the output
// shape, so there is nothing for those flags to act on.
func newExtensionCommand(deps Dependencies, ext *extension) *cobra.Command {
	use := ext.Name
	for _, p := range ext.Params {
		use += " <" + p + ">"
	}
	hasTemplate := ext.ResponseTemplate != nil

	var output, selectFields string
	var filterArgs []string

	cmd := &cobra.Command{
		Use:   use,
		Short: ext.Description,
		Args:  cobra.ExactArgs(len(ext.Params)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !hasTemplate {
				if output != "table" && output != "json" && output != "csv" && output != "tsv" {
					return errors.New(`--output must be "table", "json", "csv", or "tsv"`)
				}
			}

			respBody, err := doExtensionRequest(cmd.Context(), deps, ext, args)
			if err != nil {
				return err
			}

			var decoded any
			if len(respBody) > 0 {
				if err := json.Unmarshal(respBody, &decoded); err != nil {
					return fmt.Errorf("extension %q: decoding response: %w", ext.Name, err)
				}
			}

			if hasTemplate {
				rendered, err := renderTemplate(ext.ResponseTemplate, decoded)
				if err != nil {
					return fmt.Errorf("extension %q: rendering response_template: %w", ext.Name, err)
				}
				_, err = io.WriteString(cmd.OutOrStdout(), rendered)
				return err
			}

			return writeExtensionRows(cmd, ext, decoded, filterArgs, selectFields, output)
		},
	}

	if !hasTemplate {
		cmd.Flags().StringVarP(&output, "output", "o", "table", `Output format: "table", "json", "csv", or "tsv"`)
		addFilterAndSelectFlags(cmd, &filterArgs, &selectFields)
	}
	return cmd
}

// doExtensionRequest renders ext's url (and token, if auth is bearer) and
// request_template (if set) against a requestContext built from args,
// resolving DymmerToken lazily and only when auth is "bearer" — extensions
// with auth: none never touch the credential store at all — then executes
// the HTTP call and returns the response body once a 2xx status has been
// confirmed. A non-2xx status is reported as an error including the status
// code and a capped snippet of the response body.
func doExtensionRequest(ctx context.Context, deps Dependencies, ext *extension, args []string) ([]byte, error) {
	reqCtx := requestContext{BaseURL: deps.BaseURL, Args: map[string]string{}}
	if reqCtx.BaseURL == "" {
		reqCtx.BaseURL = defaultBaseURL
	}
	for i, p := range ext.Params {
		reqCtx.Args[p] = args[i]
	}
	if ext.Auth == "bearer" {
		token, _, err := credentials.Resolve(deps.Env, deps.Store)
		if err != nil {
			return nil, errors.New(noCredentialsMessage)
		}
		reqCtx.DymmerToken = token
	}

	urlStr, err := renderTemplate(ext.URLTemplate, reqCtx)
	if err != nil {
		return nil, fmt.Errorf("extension %q: rendering url: %w", ext.Name, err)
	}

	var body []byte
	if ext.RequestTemplate != nil {
		rendered, err := renderTemplate(ext.RequestTemplate, reqCtx)
		if err != nil {
			return nil, fmt.Errorf("extension %q: rendering request_template: %w", ext.Name, err)
		}
		if !json.Valid([]byte(rendered)) {
			return nil, fmt.Errorf("extension %q: request_template did not render valid JSON", ext.Name)
		}
		body = []byte(rendered)
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, ext.Method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("extension %q: building request: %w", ext.Name, err)
	}
	if len(body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if ext.Auth == "bearer" {
		tokenStr, err := renderTemplate(ext.TokenTemplate, reqCtx)
		if err != nil {
			return nil, fmt.Errorf("extension %q: rendering token: %w", ext.Name, err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+tokenStr)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("extension %q: request to %s failed: %w", ext.Name, urlStr, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("extension %q: reading response: %w", ext.Name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := respBody
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		return nil, fmt.Errorf("extension %q: %s returned status %d: %s", ext.Name, urlStr, resp.StatusCode, string(snippet))
	}
	return respBody, nil
}

// writeExtensionRows implements the response_path pipeline: resolve
// response_path (or treat the whole decoded body as the row array when
// unset), require the target to be a JSON array, wrap scalar elements as
// {"value": elem}, apply --filter/--select, and render via the same
// writeSelectedTable/JSON/Delimited helpers the built-in list commands use.
func writeExtensionRows(cmd *cobra.Command, ext *extension, decoded any, filterArgs []string, selectFields, output string) error {
	target := decoded
	pathDesc := ext.ResponsePath
	if pathDesc == "" {
		pathDesc = "(response body)"
	}
	if ext.ResponsePath != "" {
		m, ok := decoded.(map[string]any)
		if !ok {
			return fmt.Errorf("extension %q: response_path %q: response body is not a JSON object", ext.Name, pathDesc)
		}
		v, ok := getPath(m, ext.ResponsePath)
		if !ok {
			return fmt.Errorf("extension %q: response_path %q did not resolve in the response", ext.Name, pathDesc)
		}
		target = v
	}

	arr, ok := target.([]any)
	if !ok {
		return fmt.Errorf("extension %q: expected an array at response_path %q, got %T", ext.Name, pathDesc, target)
	}
	rows := make([]map[string]any, 0, len(arr))
	for _, elem := range arr {
		if m, ok := elem.(map[string]any); ok {
			rows = append(rows, m)
		} else {
			rows = append(rows, map[string]any{"value": elem})
		}
	}

	filters, err := parseFilters(filterArgs)
	if err != nil {
		return err
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if matchesFilters(row, filters) {
			filtered = append(filtered, row)
		}
	}
	rows = filtered

	fields := parseSelect(selectFields)
	if len(fields) == 0 {
		fields = defaultFieldsFromRows(rows)
	}

	switch output {
	case "csv", "tsv":
		comma := ','
		if output == "tsv" {
			comma = '\t'
		}
		return writeSelectedDelimited(cmd.OutOrStdout(), rows, fields, comma)
	case "json":
		return writeSelectedJSON(cmd.OutOrStdout(), rows, fields, true)
	default:
		return writeSelectedTable(cmd.OutOrStdout(), rows, fields, "No results found.")
	}
}
