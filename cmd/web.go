package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/dymmer-code/dym/internal/api"
	"github.com/spf13/cobra"
)

// webPollInterval is how often --wait asks the web server's state. The web servers pull their configuration every
// few minutes, so a short interval would only be noise. A variable so that tests do not wait.
var webPollInterval = 10 * time.Second

const defaultWebWait = 8 * time.Minute

var webTLSModes = map[string]bool{"required": true, "enabled": true, "disabled": true}

// newWebCommand builds "dym web", for what is not tied to a domain (the web servers one can use).
func newWebCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "web", Short: "Web servers"}
	cmd.AddCommand(newWebServersCommand(deps))
	return cmd
}

func newWebServersCommand(deps Dependencies) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "servers",
		Short: "List the web servers you can use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkWebOutput(output); err != nil {
				return err
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			servers, err := client.ListWebServers(cmd.Context())
			if err != nil {
				return wrapAuthError(err)
			}
			if output == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(servers)
			}
			if len(servers) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No web servers found.")
				return err
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tHOSTNAME\tMAINTENANCE\tID")
			for _, s := range servers {
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\n", s.Name, s.Hostname, s.Maintenance, s.ID)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", `Output format: "table" or "json"`)
	return cmd
}

// webFlags are the options every write command shares.
type webFlags struct {
	server, output string
	dryRun, wait   bool
	timeout        time.Duration
}

func addWebFlags(cmd *cobra.Command, f *webFlags) {
	cmd.Flags().StringVar(&f.server, "server", "", "The web server (name, hostname or id); not needed when the domain can only use one")
	cmd.Flags().StringVarP(&f.output, "output", "o", "table", `Output format: "table" or "json"`)
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Check it and show the answer without writing anything")
}

func addWebWaitFlags(cmd *cobra.Command, f *webFlags) {
	cmd.Flags().BoolVar(&f.wait, "wait", false, "Wait until the web server applies it (or refuses it, and say why)")
	cmd.Flags().DurationVar(&f.timeout, "timeout", defaultWebWait, "How long --wait waits")
}

// webContentFlags are the ones that say what a URI serves.
type webContentFlags struct {
	uri, kind, target, tls string
	status                 int
	noSSL                  bool
}

var webRedirectStatuses = map[int]bool{301: true, 302: true, 307: true, 308: true}

func addWebContentFlags(cmd *cobra.Command, f *webContentFlags) {
	cmd.Flags().StringVar(&f.uri, "uri", "", "The path to serve (default /)")
	cmd.Flags().StringVar(&f.kind, "type", "", "proxy (default), static or redirect")
	cmd.Flags().StringVar(&f.target, "target", "", "For a proxy http://host[:port] of a connected host of yours; for static garage://bucket/prefix/; for a redirect https://host[/path] (the path and query of the request are kept)")
	cmd.Flags().IntVar(&f.status, "status", 0, "For a redirect: 301 (permanent), 302 (temporary, the default), 307 (temporary, keeps the method) or 308 (permanent, keeps the method)")
	cmd.Flags().StringVar(&f.tls, "tls", "", "required (default: HTTP redirects to HTTPS), enabled (both) or disabled (HTTP only)")
	cmd.Flags().BoolVar(&f.noSSL, "no-ssl", false, "The same as --tls disabled")
}

// fields keeps only what was set, so that the server's defaults stay the server's.
func (f *webContentFlags) fields(cmd *cobra.Command) (map[string]string, error) {
	fields := map[string]string{}
	set := func(flag, key, value string) {
		if cmd.Flags().Changed(flag) {
			fields[key] = value
		}
	}
	set("uri", "uri", f.uri)
	set("type", "type", f.kind)
	set("target", "target", f.target)
	if cmd.Flags().Changed("status") {
		if !webRedirectStatuses[f.status] {
			return nil, errors.New("--status must be 301, 302, 307 or 308")
		}
		fields["redirect_status"] = strconv.Itoa(f.status)
	}
	if f.noSSL && cmd.Flags().Changed("tls") {
		return nil, errors.New("--no-ssl and --tls say the same thing: use only one")
	}
	if f.noSSL {
		fields["tls"] = "disabled"
	} else {
		set("tls", "tls", f.tls)
	}
	if tls, ok := fields["tls"]; ok && !webTLSModes[tls] {
		return nil, errors.New(`--tls must be "required", "enabled" or "disabled"`)
	}
	return fields, nil
}

func checkWebOutput(output string) error {
	if output != "table" && output != "json" {
		return errors.New(`--output must be "table" or "json"`)
	}
	return nil
}

func newDomainWebCommand(domain string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "web", Short: "Manage the web configuration of the names of the domain"}
	cmd.AddCommand(newWebAddCommand(domain, deps))
	cmd.AddCommand(newWebListCommand(domain, deps))
	cmd.AddCommand(newWebUpdateCommand(domain, deps))
	cmd.AddCommand(newWebRemoveCommand(domain, deps))
	cmd.AddCommand(newWebRestoreCommand(domain, deps))
	cmd.AddCommand(newWebOptionsCommand(domain, deps))
	return cmd
}

func newWebAddCommand(domain string, deps Dependencies) *cobra.Command {
	var wf webFlags
	var cf webContentFlags
	cmd := &cobra.Command{
		Use:   "add [host]",
		Short: "Add a URI to a name (the apex when no host is given)",
		Long: "Adds a URI to a name of the domain: the host is a label (api), a full name (api.example.org) or nothing for the apex.\n" +
			"The name must be an A, AAAA or CNAME record of the domain. The web server applies it within minutes: use --wait to\n" +
			"see when, or why it refused it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkWebOutput(wf.output); err != nil {
				return err
			}
			if !cmd.Flags().Changed("target") {
				return errors.New("--target is required")
			}
			fields, err := cf.fields(cmd)
			if err != nil {
				return err
			}
			host := ""
			if len(args) == 1 {
				host = args[0]
				fields["host"] = host
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			result, err := client.CreateWebURI(cmd.Context(), domain, wf.server, fields, wf.dryRun)
			if err != nil {
				return wrapAuthError(err)
			}
			return finishWeb(cmd, client, domain, host, wf, result)
		},
	}
	addWebFlags(cmd, &wf)
	addWebWaitFlags(cmd, &wf)
	addWebContentFlags(cmd, &cf)
	return cmd
}

func newWebUpdateCommand(domain string, deps Dependencies) *cobra.Command {
	var wf webFlags
	var cf webContentFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Change what a URI serves",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkWebOutput(wf.output); err != nil {
				return err
			}
			fields, err := cf.fields(cmd)
			if err != nil {
				return err
			}
			if len(fields) == 0 {
				return errors.New("nothing to change: give --uri, --type, --target, --tls or --no-ssl")
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			result, err := client.UpdateWebURI(cmd.Context(), domain, wf.server, args[0], fields, wf.dryRun)
			if err != nil {
				return wrapAuthError(err)
			}
			return finishWeb(cmd, client, domain, "", wf, result)
		},
	}
	addWebFlags(cmd, &wf)
	addWebWaitFlags(cmd, &wf)
	addWebContentFlags(cmd, &cf)
	return cmd
}

func newWebRemoveCommand(domain string, deps Dependencies) *cobra.Command {
	var wf webFlags
	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a URI (it is kept for a while and can be restored)",
		Long: "Removes a URI. The root (/) of a name cannot be removed while the name has other URIs.\n" +
			"What is removed is kept for a while: dym domain <domain> web restore <id> brings it back.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkWebOutput(wf.output); err != nil {
				return err
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			result, err := client.DeleteWebURI(cmd.Context(), domain, wf.server, args[0], wf.dryRun)
			if err != nil {
				return wrapAuthError(err)
			}
			if wf.output == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			note := ""
			if result.DryRun {
				note = " (dry run: nothing was written)"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s %s%s\n", result.URI.URI, result.URI.ID, note)
			return err
		},
	}
	addWebFlags(cmd, &wf)
	return cmd
}

func newWebRestoreCommand(domain string, deps Dependencies) *cobra.Command {
	var wf webFlags
	cmd := &cobra.Command{
		Use:   "restore <id>",
		Short: "Bring back a removed URI, if nobody took its place meanwhile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkWebOutput(wf.output); err != nil {
				return err
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			result, err := client.RestoreWebURI(cmd.Context(), domain, wf.server, args[0])
			if err != nil {
				return wrapAuthError(err)
			}
			return finishWeb(cmd, client, domain, "", wf, result)
		},
	}
	addWebFlags(cmd, &wf)
	addWebWaitFlags(cmd, &wf)
	return cmd
}

func newWebListCommand(domain string, deps Dependencies) *cobra.Command {
	var wf webFlags
	cmd := &cobra.Command{
		Use:   "list [host]",
		Short: "List the names with their URIs and where each stands",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkWebOutput(wf.output); err != nil {
				return err
			}
			host := ""
			if len(args) == 1 {
				host = args[0]
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			sites, err := client.ListWebSites(cmd.Context(), domain, wf.server, host)
			if err != nil {
				return wrapAuthError(err)
			}
			if wf.output == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(sites)
			}
			return writeWebSitesTable(cmd.OutOrStdout(), sites)
		},
	}
	cmd.Flags().StringVar(&wf.server, "server", "", "The web server (name, hostname or id); not needed when the domain can only use one")
	cmd.Flags().StringVarP(&wf.output, "output", "o", "table", `Output format: "table" or "json"`)
	return cmd
}

// finishWeb prints what the server answered to a write: the warnings on stderr, and the URI (after waiting for the web
// server to apply it, with --wait; a dry run has nothing to wait for).
func finishWeb(cmd *cobra.Command, client APIClient, zone, host string, wf webFlags, result *api.WebResult) error {
	for _, warning := range result.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning)
	}
	uri := result.URI
	if wf.wait && !result.DryRun {
		waited, err := waitForWebServer(cmd, client, zone, wf.server, host, uri.ID, wf.timeout)
		if err != nil {
			return err
		}
		uri = waited
	}
	if wf.output == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(api.WebResult{Status: result.Status, URI: uri, Warnings: result.Warnings, DryRun: result.DryRun})
	}
	if result.DryRun {
		fmt.Fprintln(cmd.OutOrStdout(), "Dry run: it was checked, nothing was written.")
	}
	return writeWebURIsTable(cmd.OutOrStdout(), []api.WebURI{uri})
}

// waitForWebServer asks the state of a URI until the web server applied it, or refused it with a reason.
func waitForWebServer(cmd *cobra.Command, client APIClient, zone, server, host, id string, timeout time.Duration) (api.WebURI, error) {
	ctx := cmd.Context()
	deadline := time.Now().Add(timeout)
	for {
		sites, err := client.ListWebSites(ctx, zone, server, host)
		if err != nil {
			return api.WebURI{}, wrapAuthError(err)
		}
		state := "pending"
		if uri, found := findWebURI(sites, id); found {
			switch uri.State {
			case "applied":
				return uri, nil
			case "errored":
				return uri, fmt.Errorf("the web server refused it: %s", uri.ErrorReason)
			}
			state = uri.State
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "waiting for the web server (state: %s)\n", state)
		if !time.Now().Add(webPollInterval).Before(deadline) {
			return api.WebURI{}, fmt.Errorf("still pending after %s: the web server has not applied it yet (it pulls its configuration every few minutes; check with \"web list\")", timeout)
		}
		select {
		case <-ctx.Done():
			return api.WebURI{}, ctx.Err()
		case <-time.After(webPollInterval):
		}
	}
}

func findWebURI(sites []api.WebSite, id string) (api.WebURI, bool) {
	for _, site := range sites {
		for _, uri := range site.URIs {
			if uri.ID == id {
				return uri, true
			}
		}
	}
	return api.WebURI{}, false
}

func writeWebURIsTable(w io.Writer, uris []api.WebURI) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tURI\tTYPE\tTARGET\tTLS\tSTATE\tREASON")
	for _, u := range uris {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", u.ID, u.URI, webKind(u), u.Target, u.TLS, u.State, webReason(u))
	}
	return tw.Flush()
}

func writeWebSitesTable(w io.Writer, sites []api.WebSite) error {
	if len(sites) == 0 {
		_, err := fmt.Fprintln(w, "No web configuration found.")
		return err
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "HOST\tURI\tTYPE\tTARGET\tTLS\tSTATE\tREASON\tID")
	for _, site := range sites {
		for _, u := range site.URIs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", site.Host, u.URI, webKind(u), u.Target, u.TLS, u.State, webReason(u), u.ID)
		}
	}
	return tw.Flush()
}

// webReason shows the reason of a refusal only while it holds: an applied URI keeps the old one in the server.
func webReason(u api.WebURI) string {
	if u.State == "errored" {
		return u.ErrorReason
	}
	return ""
}

func newWebOptionsCommand(domain string, deps Dependencies) *cobra.Command {
	var wf webFlags
	var hsts, blog, index, retired bool
	var extensions, message string
	cmd := &cobra.Command{
		Use:   "options [host]",
		Short: "Show or change the options of a name (the apex when no host is given)",
		Long: "Without options, shows the ones the name has. With them, changes only the ones given.\n" +
			"  --hsts               tell the browsers to use HTTPS only (every URI of the name needs tls required)\n" +
			"  --blog-fallback      what a static site does not have is answered with its /index.html\n" +
			"  --index-fallback     the same, with no exception for the assets (not with --blog-fallback)\n" +
			"  --asset-extensions   with --blog-fallback, extensions that stay a real 404, as jpg|png|css\n" +
			"  --retired-landing    for a retired site: what a static site does not have goes to / (302) and writes get a 410\n" +
			"  --retired-message    the text of that 410 (up to 200 characters, no quotes, backticks, braces or backslashes)\n" +
			"Use --hsts=false to take an option away; --asset-extensions \"\" does it for the extensions.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkWebOutput(wf.output); err != nil {
				return err
			}
			host := ""
			if len(args) == 1 {
				host = args[0]
			}
			changes := map[string]any{}
			for flag, key := range map[string]string{"hsts": "hsts", "blog-fallback": "blog_fallback", "index-fallback": "index_fallback", "retired-landing": "retired_landing"} {
				if cmd.Flags().Changed(flag) {
					value, _ := cmd.Flags().GetBool(flag)
					if value {
						changes[key] = true
					} else {
						changes[key] = nil
					}
				}
			}
			if cmd.Flags().Changed("asset-extensions") {
				if extensions == "" {
					changes["asset_extensions"] = nil
				} else {
					changes["asset_extensions"] = extensions
				}
			}
			if cmd.Flags().Changed("retired-message") {
				if message == "" {
					changes["retired_message"] = nil
				} else {
					changes["retired_message"] = message
				}
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			var result *api.WebOptions
			if len(changes) == 0 {
				result, err = client.GetWebOptions(cmd.Context(), domain, wf.server, host)
			} else {
				result, err = client.SetWebOptions(cmd.Context(), domain, wf.server, host, changes, wf.dryRun)
			}
			if err != nil {
				return wrapAuthError(err)
			}
			if wf.output == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if result.DryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "Dry run: it was checked, nothing was written.")
			}
			return writeWebOptionsTable(cmd.OutOrStdout(), result)
		},
	}
	addWebFlags(cmd, &wf)
	cmd.Flags().BoolVar(&hsts, "hsts", false, "Tell the browsers to use HTTPS only")
	cmd.Flags().BoolVar(&blog, "blog-fallback", false, "Answer what a static site does not have with its /index.html")
	cmd.Flags().BoolVar(&index, "index-fallback", false, "The same, with no exception for the assets")
	cmd.Flags().BoolVar(&retired, "retired-landing", false, "For a retired site: what a static site does not have goes to / (302) and writes get a 410")
	cmd.Flags().StringVar(&message, "retired-message", "", "The text of that 410")
	cmd.Flags().StringVar(&extensions, "asset-extensions", "", "With --blog-fallback, extensions that stay a real 404 (jpg|png|css)")
	return cmd
}

func writeWebOptionsTable(w io.Writer, result *api.WebOptions) error {
	if len(result.Options) == 0 {
		_, err := fmt.Fprintf(w, "%s has no options.\n", result.Host)
		return err
	}
	keys := make([]string, 0, len(result.Options))
	for key := range result.Options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "HOST\tOPTION\tVALUE")
	for _, key := range keys {
		fmt.Fprintf(tw, "%s\t%s\t%v\n", result.Host, key, result.Options[key])
	}
	return tw.Flush()
}

// webKind is the type, with the code of a redirect next to it (redirect 301).
func webKind(u api.WebURI) string {
	if u.Type == "redirect" && u.RedirectStatus != 0 {
		return fmt.Sprintf("redirect %d", u.RedirectStatus)
	}
	return u.Type
}
