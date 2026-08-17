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
	"strings"
	"time"

	"github.com/dymmer-code/dym/internal/credentials"
	"github.com/spf13/cobra"
)

// maxExtensionResponseBytes bounds how much of an extension's HTTP response
// body is ever buffered into memory, on both the success and error paths.
// url is user-authored and can point anywhere, so an unbounded io.ReadAll
// on an attacker- or accident-controlled endpoint (e.g. an 8 MiB error page)
// is a real, avoidable memory risk. This ceiling is generous for any JSON
// API response this pipeline is meant to render as a table.
const maxExtensionResponseBytes = 512 * 1024

// redactedTokenPlaceholder replaces DymmerToken when rendering a URL/error
// strictly for display (never for the actual outgoing request), so a
// bearer-authenticated extension whose url template embeds
// "{{.DymmerToken}}" (e.g. a query-param-authenticated endpoint) never
// leaks the real token into an error message.
const redactedTokenPlaceholder = "<redacted>"

// newExtCommand builds the "dym ext" parent command: a user-defined escape
// hatch for arbitrary HTTP calls declared in an extensions.yaml file (see
// cmd/extensions.go for the schema and loading/validation).
//
// Cobra needs to know, at tree-build time, which subcommands and flags
// (--filter/--select/--output) to register per extension — but which
// extensions.yaml to read depends on a --extensions-file flag that can
// appear anywhere in the remaining args, which cobra can't resolve before
// building the tree in the first place. cmd/domain.go's newDomainCommand
// solves the structurally identical problem (a dynamic domain name that has
// to be known before the rest of the tree can be built) with
// DisableFlagParsing + a RunE that builds the real subtree at run time and
// dispatches into it; this does the same. One consequence (shared with
// newDomainCommand) is that this file's I/O now happens lazily, inside
// RunE, only when "dym ext ..." is actually invoked — NewRootCommand itself
// never touches the filesystem for this, keeping every other command's
// tests (and every other command) hermetic regardless of what extensions
// file does or doesn't exist on the machine running them.
func newExtCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ext",
		Short: "Run user-defined HTTP extensions (see extensions.yaml)",
		Long: "Run user-defined HTTP extensions declared in an extensions.yaml file.\n\n" +
			"Use --extensions-file (-e) to point at a specific file for this invocation; " +
			"otherwise DYM_EXTENSIONS_FILE, then the OS default config location, is used.",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flagValue, remaining := splitExtensionsFileFlag(args)
			path := resolveExtensionsFilePath(deps, flagValue)
			load := loadExtensionsFile(path)

			// A broken file (unparseable YAML, or an unknown key under
			// strict decoding) means nothing was loaded at all: fail loudly
			// here, before ever consulting the (empty) subtree, so
			// "dym ext <anything>" can't silently fall through to cobra's
			// default help-and-exit-0 behavior for a missing subcommand.
			if load.FileErr != nil {
				return fmt.Errorf("failed to load extensions file %s: %w", path, load.FileErr)
			}
			// A requested extension that exists in the file but failed its
			// own validation gets a specific, actionable error (naming why
			// it was skipped) instead of the generic "unknown command"
			// cobra would otherwise produce once dispatched below.
			if len(remaining) > 0 {
				if reason, ok := load.skippedReason(remaining[0]); ok {
					return fmt.Errorf("extension %q failed to load from %s: %s; run \"dym ext list\" for details", remaining[0], path, reason)
				}
			}

			nested := newExtSubtree(deps, path, load)
			nested.SetOut(cmd.OutOrStdout())
			nested.SetErr(cmd.ErrOrStderr())
			nested.SetArgs(remaining)
			return nested.Execute()
		},
	}
	return cmd
}

// splitExtensionsFileFlag scans args for a "--extensions-file"/"-e" flag
// (long form "--extensions-file value" or "--extensions-file=value", short
// form "-e value" or "-e=value"), removes every recognized occurrence, and
// returns the last one found (plain, non-repeatable-flag semantics — the
// same "last occurrence wins" behavior a real pflag.StringVar flag would
// have) alongside every other argument, untouched and in original order,
// for forwarding into the extension subtree's own completely normal cobra
// flag parsing (--filter/--select/--output, --help, positional args, etc.).
//
// A literal "--" argument unconditionally ends flag scanning: it, and
// everything after it, is passed through untouched with no further
// inspection — including any "--extensions-file" text that appears after it,
// which must be treated as ordinary positional data, not our flag, exactly
// as "--" behaves everywhere else in this codebase's/cobra's own parsing.
//
// This is hand-rolled rather than built on pflag's UnknownFlags allowlist
// mode because that mode does not do passthrough: pflag.parseLongArg, on an
// unrecognized flag, discards it *and* silently strips what it assumes is
// that flag's value (the following non-flag token) from the returned args
// entirely — so a naive `fs.ParseErrorsAllowlist.UnknownFlags = true` setup
// would eat e.g. "--filter type=A" whole rather than forwarding it to the
// nested tree, which is the opposite of what's needed here.
func splitExtensionsFileFlag(args []string) (value string, remaining []string) {
	const long = "--extensions-file"
	remaining = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			remaining = append(remaining, args[i:]...)
			break
		}
		switch {
		case a == long || a == "-e":
			if i+1 < len(args) {
				value = args[i+1]
				i++
				continue
			}
			// No value follows; leave the bare flag in place so the nested
			// tree's own (real) flag parser produces its usual
			// "flag needs an argument" error instead of us silently
			// swallowing it.
			remaining = append(remaining, a)
		case strings.HasPrefix(a, long+"="):
			value = strings.TrimPrefix(a, long+"=")
		case strings.HasPrefix(a, "-e="):
			value = strings.TrimPrefix(a, "-e=")
		default:
			remaining = append(remaining, a)
		}
	}
	return value, remaining
}

// newExtSubtree builds the actual "list" + one-per-extension command tree
// for an already-resolved, already-loaded extensions file. Its own
// error/usage printing is silenced so the single outer "dym" root command
// prints any resulting error exactly once (same pattern as
// newDomainResourceTree/newProjectResourceTree).
func newExtSubtree(deps Dependencies, path string, load extensionsLoad) *cobra.Command {
	root := &cobra.Command{
		Use:               "ext-resources",
		Annotations:       map[string]string{cobra.CommandDisplayNameAnnotation: "dym ext"},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		SilenceUsage:      true,
		SilenceErrors:     true,
	}
	root.AddCommand(newExtListCommand(path, load))

	names := make([]string, 0, len(load.Extensions))
	for name := range load.Extensions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		root.AddCommand(newExtensionCommand(deps, load.Extensions[name]))
	}
	return root
}

// newExtListCommand builds "dym ext list". A missing extensions file, or a
// file that parses but declares zero (usable) extensions, is reported as
// plain informational output on stdout with a zero exit code — "you haven't
// set any extensions up" is a normal state, not an error. (A file that
// fails to parse at all never reaches here: newExtCommand's RunE returns
// load.FileErr directly, before this subtree is even built.)
func newExtListCommand(path string, load extensionsLoad) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available extensions",
		RunE: func(cmd *cobra.Command, _ []string) error {
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
					fmt.Fprintf(out, "  %s: %s\n", s.Name, s.Reason)
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
			// --output/--filter/--select are validated up front, before any
			// HTTP call, exactly like every native list command
			// (cmd/domain.go's newRecordsListCommand parses/validates
			// before resolveAPI): a typo'd --filter must not let a
			// mutating POST fire first.
			var filters []fieldFilter
			var fields []string
			if !hasTemplate {
				if output != "table" && output != "json" && output != "csv" && output != "tsv" {
					return errors.New(`--output must be "table", "json", "csv", or "tsv"`)
				}
				var err error
				filters, err = parseFilters(filterArgs)
				if err != nil {
					return err
				}
				fields = parseSelect(selectFields)
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

			return writeExtensionRows(cmd, ext, decoded, filters, fields, output)
		},
	}

	if !hasTemplate {
		cmd.Flags().StringVarP(&output, "output", "o", "table", `Output format: "table", "json", "csv", or "tsv"`)
		addFilterAndSelectFlags(cmd, &filterArgs, &selectFields)
	}
	return cmd
}

// sanitizeExtensionError returns a plain error whose message is err's
// message with every occurrence of realURL replaced by redactedURL. This is
// used instead of "%w"-wrapping err directly: a *url.Error (returned by
// http.NewRequest/http.Client.Do on a malformed URL or failed request)
// embeds the exact URL it was given inside its own Error() string, so even
// an outer message that only mentions the redacted URL would still leak the
// real (possibly token-bearing) one the moment err gets wrapped with %w and
// printed/unwrapped. Returning a fresh error discards that embedded field
// entirely rather than trying to keep it reachable-but-safe.
func sanitizeExtensionError(err error, realURL, redactedURL string) error {
	msg := err.Error()
	if realURL != "" && realURL != redactedURL {
		msg = strings.ReplaceAll(msg, realURL, redactedURL)
	}
	return errors.New(msg)
}

// doExtensionRequest renders ext's url (and token, if auth is bearer) and
// request_template (if set) against a requestContext built from args,
// resolving DymmerToken lazily and only when auth is "bearer" — extensions
// with auth: none never touch the credential store at all — then executes
// the HTTP call and returns the response body once a 2xx status has been
// confirmed. A non-2xx status is reported as an error including the status
// code and a capped snippet of the response body; a 401 from a bearer-auth
// extension gets the same "run dym auth login" guidance every native
// command's wrapAuthError adds. Every error message that could otherwise
// reference the rendered url (which may itself embed the real bearer token,
// e.g. a query-param-authenticated endpoint) uses a redacted stand-in
// instead — see sanitizeExtensionError.
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

	// A redacted stand-in for urlStr, used in every error message from here
	// on so a token embedded in the URL itself never leaks. If re-rendering
	// with a redacted token somehow fails (it shouldn't — the same template
	// just rendered successfully above), fall back to a generic placeholder
	// rather than risk falling back to the real URL.
	redactedURL := "<url unavailable>"
	redactedCtx := reqCtx
	redactedCtx.DymmerToken = redactedTokenPlaceholder
	if r, rErr := renderTemplate(ext.URLTemplate, redactedCtx); rErr == nil {
		redactedURL = r
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
		return nil, fmt.Errorf("extension %q: building request to %s: %w", ext.Name, redactedURL, sanitizeExtensionError(err, urlStr, redactedURL))
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
		return nil, fmt.Errorf("extension %q: request to %s failed: %w", ext.Name, redactedURL, sanitizeExtensionError(err, urlStr, redactedURL))
	}
	defer resp.Body.Close()

	// Bound how much of the body we ever buffer (see
	// maxExtensionResponseBytes) regardless of status code: url is
	// user-authored and can point anywhere.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxExtensionResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("extension %q: reading response from %s: %w", ext.Name, redactedURL, sanitizeExtensionError(err, urlStr, redactedURL))
	}
	truncated := len(respBody) > maxExtensionResponseBytes
	if truncated {
		respBody = respBody[:maxExtensionResponseBytes]
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := respBody
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		baseErr := fmt.Errorf("extension %q: %s returned status %d: %s", ext.Name, redactedURL, resp.StatusCode, string(snippet))
		if resp.StatusCode == http.StatusUnauthorized && ext.Auth == "bearer" {
			return nil, fmt.Errorf(`%w; run "dym auth login" or set DYMMER_TOKEN`, baseErr)
		}
		return nil, baseErr
	}
	if truncated {
		return nil, fmt.Errorf("extension %q: response from %s exceeded %d bytes; refusing to buffer it in full", ext.Name, redactedURL, maxExtensionResponseBytes)
	}
	return respBody, nil
}

// writeExtensionRows implements the response_path pipeline: resolve
// response_path (or treat the whole decoded body as the row array when
// unset), require the target to be a JSON array, wrap scalar elements as
// {"value": elem}, apply the already-parsed filters/fields, and render via
// the same writeSelectedTable/JSON/Delimited helpers the built-in list
// commands use. filters/fields are parsed by the caller before the HTTP
// call fires (see newExtensionCommand); fields being empty here means
// --select was not given, so the default column set is computed from the
// rows actually returned.
func writeExtensionRows(cmd *cobra.Command, ext *extension, decoded any, filters []fieldFilter, fields []string, output string) error {
	target := decoded
	if ext.ResponsePath != "" {
		m, ok := decoded.(map[string]any)
		if !ok {
			return fmt.Errorf("extension %q: response_path %q: response body is not a JSON object", ext.Name, ext.ResponsePath)
		}
		v, ok := getPath(m, ext.ResponsePath)
		if !ok {
			return fmt.Errorf("extension %q: response_path %q did not resolve in the response", ext.Name, ext.ResponsePath)
		}
		target = v
	}

	arr, ok := target.([]any)
	if !ok {
		if ext.ResponsePath == "" {
			return fmt.Errorf("extension %q: expected the response body to be a JSON array, got %T", ext.Name, target)
		}
		return fmt.Errorf("extension %q: expected an array at response_path %q, got %T", ext.Name, ext.ResponsePath, target)
	}
	rows := make([]map[string]any, 0, len(arr))
	for _, elem := range arr {
		if m, ok := elem.(map[string]any); ok {
			rows = append(rows, m)
		} else {
			rows = append(rows, map[string]any{"value": elem})
		}
	}

	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if matchesFilters(row, filters) {
			filtered = append(filtered, row)
		}
	}
	rows = filtered

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
