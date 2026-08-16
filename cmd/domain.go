package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/dymmer-code/dym/internal/api"
	"github.com/dymmer-code/dym/internal/credentials"
	"github.com/spf13/cobra"
)

// APIClient is the subset of *api.Client the domain, mailbox, forwarding,
// and (in a later task) project commands depend on. Tests inject a fake
// implementation; production code resolves a real *api.Client lazily.
type APIClient interface {
	ListRecords(ctx context.Context, domain, recordType string) ([]api.Record, error)
	CreateRecord(ctx context.Context, domain string, input api.RecordInput) (*api.Record, error)
	UpdateRecord(ctx context.Context, domain, id string, input api.RecordInput) (*api.Record, error)
	DeleteRecord(ctx context.Context, domain, id string) (*api.Record, error)
	ListMailboxes(ctx context.Context, domain string) ([]api.Mailbox, error)
	ListForwardings(ctx context.Context, domain string) ([]api.Forwarding, error)
	GetSecrets(ctx context.Context, project, env, deployment, format string) (*api.SecretsResult, error)
}

const defaultBaseURL = "https://dymmer.com/api/v1"

const noCredentialsMessage = `no Dymmer API credentials available; run "dym auth login" or set DYMMER_TOKEN`

// resolveAPI returns deps.API when a test (or caller) has already injected
// one, otherwise it resolves stored/env credentials and builds a real
// *api.Client. This must only be called from inside a command's RunE, never
// from NewRootCommand, so that credential-free commands like "dym auth
// login" keep working with no stored token at all.
func resolveAPI(deps Dependencies) (APIClient, error) {
	if deps.API != nil {
		return deps.API, nil
	}
	token, _, err := credentials.Resolve(deps.Env, deps.Store)
	if err != nil {
		return nil, errors.New(noCredentialsMessage)
	}
	baseURL := deps.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return api.NewClient(baseURL, token, nil), nil
}

// wrapAuthError adds actionable login guidance to a 401 response from the
// API, on top of the underlying (already-redacted) *api.APIError message.
func wrapAuthError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf(`%w; run "dym auth login" or set DYMMER_TOKEN`, err)
	}
	return err
}

// newDomainCommand builds the "dym domain <domain> <resource> <verb>" entry
// point. Cobra cannot natively route a dynamic positional token (the domain
// name) in the middle of a command path, so this command disables its own
// flag parsing, takes every remaining argument itself, peels off the domain
// name, and executes a small nested command tree (records/mailboxes/
// forwardings) against the rest — closing over the domain name and deps.
func newDomainCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "domain <domain> <resource> <verb> [args]",
		Short:              "Manage DNS records, mailboxes, and forwardings for a domain",
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := args[0]
			nested := newDomainResourceTree(domain, deps)
			nested.SetOut(cmd.OutOrStdout())
			nested.SetErr(cmd.ErrOrStderr())
			nested.SetArgs(args[1:])
			return nested.Execute()
		},
	}
	return cmd
}

// newDomainResourceTree builds the records/mailboxes/forwardings command
// tree for a single, already-known domain name. Its own error/usage
// printing is silenced so the single outer "dym" root command prints any
// resulting error exactly once.
func newDomainResourceTree(domain string, deps Dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "domain-resources",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRecordsCommand(domain, deps))
	root.AddCommand(newMailboxesCommand(domain, deps))
	root.AddCommand(newForwardingsCommand(domain, deps))
	return root
}

func newRecordsCommand(domain string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "records", Short: "Manage DNS records"}
	cmd.AddCommand(newRecordsListCommand(domain, deps))
	cmd.AddCommand(newRecordsCreateCommand(domain, deps))
	cmd.AddCommand(newRecordsUpdateCommand(domain, deps))
	cmd.AddCommand(newRecordsDeleteCommand(domain, deps))
	return cmd
}

func newRecordsListCommand(domain string, deps Dependencies) *cobra.Command {
	var recordType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List DNS records",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			records, err := client.ListRecords(cmd.Context(), domain, recordType)
			if err != nil {
				return wrapAuthError(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(records)
		},
	}
	cmd.Flags().StringVar(&recordType, "type", "", "Filter by record type (e.g. A)")
	return cmd
}

// recordFlags holds every CLI flag records create/update accept. Only flags
// the user actually set are copied into the api.RecordInput sent to the
// server, so unset flags never overwrite content with empty strings.
type recordFlags struct {
	recordType   string
	name         string
	ttl          int
	ip           string
	ipv6         string
	alias        string
	mailProvider string
	priority     string
	ns           string
	value        string
	port         string
	weight       string
	target       string
}

func addRecordFlags(cmd *cobra.Command, f *recordFlags) {
	cmd.Flags().StringVar(&f.recordType, "type", "", "Record type (e.g. A, AAAA, CNAME, MX, NS, TXT, SRV)")
	cmd.Flags().StringVar(&f.name, "name", "", "Record hostname/label (omit for the domain apex)")
	cmd.Flags().IntVar(&f.ttl, "ttl", 0, "Time to live in seconds (omit to use the server default)")
	cmd.Flags().StringVar(&f.ip, "ip", "", "IPv4 address (A records)")
	cmd.Flags().StringVar(&f.ipv6, "ipv6", "", "IPv6 address (AAAA records)")
	cmd.Flags().StringVar(&f.alias, "alias", "", "Target hostname (CNAME records)")
	cmd.Flags().StringVar(&f.mailProvider, "mail-provider", "", "Mail provider hostname (MX records)")
	cmd.Flags().StringVar(&f.priority, "priority", "", "Priority (MX, SRV records)")
	cmd.Flags().StringVar(&f.ns, "ns", "", "Name server (NS records)")
	cmd.Flags().StringVar(&f.value, "value", "", "Text value (TXT records)")
	cmd.Flags().StringVar(&f.port, "port", "", "Port (SRV records)")
	cmd.Flags().StringVar(&f.weight, "weight", "", "Weight (SRV records)")
	cmd.Flags().StringVar(&f.target, "target", "", "Target hostname (SRV records)")
}

// toInput builds an api.RecordInput from f, including only content flags
// that were actually set on cmd (cmd.Flags().Changed), and translating an
// unset --ttl into a nil *int rather than a sent "0".
func (f *recordFlags) toInput(cmd *cobra.Command) api.RecordInput {
	input := api.RecordInput{Type: f.recordType, Host: f.name}
	if cmd.Flags().Changed("ttl") {
		ttl := f.ttl
		input.TTL = &ttl
	}
	content := map[string]string{}
	setIfChanged := func(flag, key, value string) {
		if cmd.Flags().Changed(flag) {
			content[key] = value
		}
	}
	setIfChanged("ip", "ip", f.ip)
	setIfChanged("ipv6", "ipv6", f.ipv6)
	setIfChanged("alias", "alias", f.alias)
	setIfChanged("mail-provider", "mail_provider", f.mailProvider)
	setIfChanged("priority", "priority", f.priority)
	setIfChanged("ns", "ns", f.ns)
	setIfChanged("value", "value", f.value)
	setIfChanged("port", "port", f.port)
	setIfChanged("weight", "weight", f.weight)
	setIfChanged("target", "target", f.target)
	if len(content) > 0 {
		input.Content = content
	}
	return input
}

func newRecordsCreateCommand(domain string, deps Dependencies) *cobra.Command {
	f := &recordFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a DNS record",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.recordType == "" {
				return errors.New("--type is required")
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			record, err := client.CreateRecord(cmd.Context(), domain, f.toInput(cmd))
			if err != nil {
				return wrapAuthError(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(record)
		},
	}
	addRecordFlags(cmd, f)
	return cmd
}

func newRecordsUpdateCommand(domain string, deps Dependencies) *cobra.Command {
	f := &recordFlags{}
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a DNS record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.recordType == "" {
				return errors.New("--type is required (resend it even if unchanged; the server needs it to pick content validation)")
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			record, err := client.UpdateRecord(cmd.Context(), domain, args[0], f.toInput(cmd))
			if err != nil {
				return wrapAuthError(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(record)
		},
	}
	addRecordFlags(cmd, f)
	return cmd
}

func newRecordsDeleteCommand(domain string, deps Dependencies) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a DNS record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !yes {
				if !deps.IsTerminal() {
					return errors.New("refusing to delete without confirmation in a non-interactive session; use --yes")
				}
				prompt := fmt.Sprintf("Delete DNS record %s from %s? [y/N]: ", id, domain)
				ok, err := deps.Confirm(prompt)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("deletion cancelled")
				}
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			record, err := client.DeleteRecord(cmd.Context(), domain, id)
			if err != nil {
				return wrapAuthError(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(record)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func newMailboxesCommand(domain string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "mailboxes", Short: "Manage mailboxes"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List mailboxes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			mailboxes, err := client.ListMailboxes(cmd.Context(), domain)
			if err != nil {
				return wrapAuthError(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(mailboxes)
		},
	})
	return cmd
}

func newForwardingsCommand(domain string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "forwardings", Short: "Manage mail forwardings"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List mail forwardings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			forwardings, err := client.ListForwardings(cmd.Context(), domain)
			if err != nil {
				return wrapAuthError(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(forwardings)
		},
	})
	return cmd
}
