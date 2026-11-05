package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/dymmer-code/dym/internal/api"
	"github.com/spf13/cobra"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const outputHelp = `Output format: "table", "json", "csv", or "tsv"`

func checkOutput(output string) error {
	if output != "table" && output != "json" && output != "csv" && output != "tsv" {
		return errors.New(`--output must be "table", "json", "csv", or "tsv"`)
	}
	return nil
}

// ── Output ────────────────────────────────────────────────────────────────

// sendSummary says how an address sends: with its own password ("credential"), through the addresses that can
// send as it, or "-" for nobody (through our server).
func sendSummary(identity api.Identity, zone string) string {
	own := strings.ToLower(identity.Username + "@" + zone)
	others := []string{}
	for _, address := range identity.Authorizations {
		if strings.ToLower(address) != own {
			others = append(others, address)
		}
	}
	switch {
	case identity.HasPassword && len(others) > 0:
		return "credential + " + strings.Join(others, ", ")
	case identity.HasPassword:
		return "credential"
	case len(others) > 0:
		return strings.Join(others, ", ")
	default:
		return "-"
	}
}

// receiveSummary says where an address receives: a mailbox with what it uses, the destinations it forwards to, or
// "-" when it does not receive.
func receiveSummary(identity api.Identity) string {
	switch {
	case identity.Mailbox != nil:
		return fmt.Sprintf("mailbox, %d MB of %s", identity.Mailbox.QuotaUsedMB, strings.ToUpper(identity.Mailbox.QuotaTier))
	case identity.Forwarding != nil:
		return "forwards to " + strings.Join(identity.Forwarding.Destination, ", ")
	default:
		return "-"
	}
}

// shortID is the beginning of an id that is enough to name it, as git does with commits: every command that takes
// an id accepts it while it is not ambiguous.
func shortID(id string) string {
	if len(id) > shortIDLength {
		return id[:shortIDLength]
	}
	return id
}

const shortIDLength = 8

// findByID returns the position of the item whose id is ref, or the only one that starts with it.
func findByID(ids []string, ref string) (int, error) {
	if ref == "" {
		return -1, errors.New("empty id")
	}
	found := -1
	for i, id := range ids {
		if id == ref {
			return i, nil
		}
		if strings.HasPrefix(id, ref) {
			if found >= 0 {
				return -1, fmt.Errorf("%q matches more than one id: write more of it", ref)
			}
			found = i
		}
	}
	return found, nil
}

func identityRow(identity api.Identity, zone string) []string {
	return []string{identity.ID, fmt.Sprintf("%t", identity.Enabled), identity.Username + "@" + zone, sendSummary(identity, zone), receiveSummary(identity)}
}

func writeIdentitiesTable(zone string) func(io.Writer, []api.Identity) error {
	return func(w io.Writer, identities []api.Identity) error {
		if len(identities) == 0 {
			_, err := fmt.Fprintln(w, "No addresses found.")
			return err
		}
		tw := newTabWriter(w)
		fmt.Fprintln(tw, "ID\tENABLED\tADDRESS\tSEND\tRECEIVE")
		for _, identity := range identities {
			row := identityRow(identity, zone)
			row[0] = shortID(row[0])
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		return tw.Flush()
	}
}

func writeIdentitiesCSV(zone string) func(io.Writer, []api.Identity, rune) error {
	return func(w io.Writer, identities []api.Identity, comma rune) error {
		rows := make([][]string, 0, len(identities))
		for _, identity := range identities {
			rows = append(rows, identityRow(identity, zone))
		}
		return writeDelimitedRows(w, rows, comma)
	}
}

func storageRow(storage api.Storage) []string {
	size := "unknown"
	if storage.UsageBytes != nil {
		size = fmt.Sprintf("%d MB", *storage.UsageBytes/(1<<20))
	}
	return []string{storage.ID, storage.Name, storage.AbandonedAt, size, fmt.Sprintf("%d", storage.DaysLeft)}
}

func writeStoragesTable(w io.Writer, storages []api.Storage) error {
	if len(storages) == 0 {
		_, err := fmt.Fprintln(w, "No deleted mailboxes to recover.")
		return err
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tNAME\tDELETED\tSIZE\tDAYS LEFT")
	for _, storage := range storages {
		row := storageRow(storage)
		row[0] = shortID(row[0])
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

func writeStoragesCSV(w io.Writer, storages []api.Storage, comma rune) error {
	rows := make([][]string, 0, len(storages))
	for _, storage := range storages {
		rows = append(rows, storageRow(storage))
	}
	return writeDelimitedRows(w, rows, comma)
}

// emit writes items in the format asked for: the table or delimited renderers of the type when no field is
// selected, or the selected fields of its JSON form.
func emit[T any](w io.Writer, output string, fields []string, items []T, rows []map[string]any, table func(io.Writer, []T) error, delimited func(io.Writer, []T, rune) error, emptyMessage string, multiple bool) error {
	if output == "csv" || output == "tsv" {
		comma := ','
		if output == "tsv" {
			comma = '\t'
		}
		if len(fields) == 0 {
			return delimited(w, items, comma)
		}
		return writeSelectedDelimited(w, rows, fields, comma)
	}
	if len(fields) == 0 {
		if output == "json" {
			if multiple {
				return json.NewEncoder(w).Encode(items)
			}
			return json.NewEncoder(w).Encode(items[0])
		}
		return table(w, items)
	}
	if output == "json" {
		return writeSelectedJSON(w, rows, fields, multiple)
	}
	return writeSelectedTable(w, rows, fields, emptyMessage)
}

// ── Input ─────────────────────────────────────────────────────────────────

// readPassword takes the password of an address from the standard input or, asking twice, from the terminal.
// There is deliberately no flag that takes it as an argument: it would be seen in the list of processes.
func readPassword(cmd *cobra.Command, deps Dependencies, fromStdin, ask bool) (*string, error) {
	switch {
	case fromStdin && ask:
		return nil, errors.New("use --password-stdin or --ask-password, not both")
	case fromStdin:
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return nil, fmt.Errorf("reading the password from the standard input: %w", err)
		}
		password := strings.TrimRight(line, "\r\n")
		if password == "" {
			return nil, errors.New("the password read from the standard input is empty")
		}
		return &password, nil
	case ask:
		first, err := deps.ReadSecret("Password: ")
		if err != nil {
			return nil, err
		}
		second, err := deps.ReadSecret("Repeat the password: ")
		if err != nil {
			return nil, err
		}
		if first != second {
			return nil, errors.New("the passwords do not match")
		}
		if first == "" {
			return nil, errors.New("the password is empty")
		}
		return &first, nil
	default:
		return nil, nil
	}
}

type identityFlags struct {
	enabled            bool
	passwordStdin      bool
	askPassword        bool
	mailbox            bool
	fromStorage        string
	forward            []string
	noReceive          bool
	rename             string
	removePassword     bool
	updateForwardings  bool
	output, selectArgs string
}

func addIdentityFlags(cmd *cobra.Command, f *identityFlags, editing bool) {
	cmd.Flags().BoolVar(&f.enabled, "enabled", true, "Whether the address works (--enabled=false to disable it)")
	cmd.Flags().BoolVar(&f.passwordStdin, "password-stdin", false, "Read the password (so the address can send, and open its mailbox) from the standard input")
	cmd.Flags().BoolVar(&f.askPassword, "ask-password", false, "Ask for the password on the terminal, without echo")
	cmd.Flags().BoolVar(&f.mailbox, "mailbox", false, "Receive in a mailbox")
	cmd.Flags().StringVar(&f.fromStorage, "from-storage", "", "With --mailbox, start from the mail of a deleted mailbox (see \"storages list\")")
	cmd.Flags().StringSliceVar(&f.forward, "forward", nil, "Forward the mail to these addresses (comma separated or repeated)")
	cmd.Flags().BoolVar(&f.noReceive, "no-receive", false, "Do not receive mail (a mailbox keeps its mail as a deleted mailbox)")
	if editing {
		cmd.Flags().StringVar(&f.rename, "rename", "", "Change the name of the address (the part before the @)")
		cmd.Flags().BoolVar(&f.removePassword, "remove-password", false, "Remove the password: it will not be able to send by itself")
		cmd.Flags().BoolVar(&f.updateForwardings, "update-forwardings", false, "With --rename, make the forwardings of the zone that sent to the old address send to the new one")
	}
	cmd.Flags().StringVarP(&f.output, "output", "o", "table", outputHelp)
	cmd.Flags().StringVar(&f.selectArgs, "select", "", selectFlagHelp)
}

// receiveFrom builds how the address receives from the flags; nil when none of them was given.
func (f *identityFlags) receiveFrom() (*api.Receive, error) {
	asked := 0
	for _, on := range []bool{f.mailbox, len(f.forward) > 0, f.noReceive} {
		if on {
			asked++
		}
	}
	switch {
	case asked > 1:
		return nil, errors.New("an address either has a mailbox or forwards, not both: use only one of --mailbox, --forward and --no-receive")
	case f.fromStorage != "" && !f.mailbox:
		return nil, errors.New("--from-storage only goes with --mailbox")
	case f.mailbox:
		return &api.Receive{Type: "mailbox", StorageID: f.fromStorage}, nil
	case len(f.forward) > 0:
		return &api.Receive{Type: "forwarding", Destination: f.forward}, nil
	case f.noReceive:
		return &api.Receive{Type: "none"}, nil
	default:
		return nil, nil
	}
}

// resolveIdentity finds an identity of zone by its id, by its address (name@zone) or by its name.
func resolveIdentity(cmd *cobra.Command, client APIClient, zone, ref string) (*api.Identity, error) {
	identities, err := client.ListIdentities(cmd.Context(), zone)
	if err != nil {
		return nil, wrapAuthError(err)
	}
	name := strings.TrimSuffix(strings.ToLower(ref), "@"+strings.ToLower(zone))
	for i := range identities {
		if identities[i].ID == ref || strings.ToLower(identities[i].Username) == name {
			return &identities[i], nil
		}
	}
	ids := make([]string, len(identities))
	for i := range identities {
		ids[i] = identities[i].ID
	}
	if i, err := findByID(ids, ref); err != nil {
		return nil, err
	} else if i >= 0 {
		return &identities[i], nil
	}
	return nil, fmt.Errorf("no address %q in %s", ref, zone)
}

// resolveCredential finds the identity that is to send as another: by id, or by address, which may be of another
// zone of yours.
func resolveCredential(cmd *cobra.Command, client APIClient, zone, ref string) (*api.Identity, error) {
	if uuidPattern.MatchString(ref) {
		return &api.Identity{ID: ref}, nil
	}
	credentialZone := zone
	if at := strings.LastIndex(ref, "@"); at > 0 {
		credentialZone = ref[at+1:]
	}
	identity, err := resolveIdentity(cmd, client, credentialZone, ref)
	if err != nil {
		return nil, err
	}
	if !identity.HasPassword {
		return nil, fmt.Errorf("%s@%s has no password, so it cannot send as another address", identity.Username, credentialZone)
	}
	return identity, nil
}

// resolveStorage finds a deleted mailbox of zone by its id or by the beginning of it.
func resolveStorage(cmd *cobra.Command, client APIClient, zone, ref string) (*api.Storage, error) {
	storages, err := client.ListStorages(cmd.Context(), zone)
	if err != nil {
		return nil, wrapAuthError(err)
	}
	ids := make([]string, len(storages))
	for i := range storages {
		ids[i] = storages[i].ID
	}
	i, err := findByID(ids, ref)
	if err != nil {
		return nil, err
	}
	if i < 0 {
		return nil, fmt.Errorf("no deleted mailbox %q in %s", ref, zone)
	}
	return &storages[i], nil
}

func confirmOrRefuse(cmd *cobra.Command, deps Dependencies, yes bool, prompt, what string) error {
	if yes {
		return nil
	}
	if !deps.IsTerminal() {
		return fmt.Errorf("refusing to %s without confirmation in a non-interactive session; use --yes", what)
	}
	ok, err := deps.Confirm(prompt)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cancelled")
	}
	return nil
}

// ── Commands ──────────────────────────────────────────────────────────────

func newIdentitiesCommand(zone string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "identities", Aliases: []string{"addresses"}, Short: "Manage the mail addresses of the zone: how they send and receive"}
	cmd.AddCommand(newIdentitiesListCommand(zone, deps))
	cmd.AddCommand(newIdentitiesAddCommand(zone, deps))
	cmd.AddCommand(newIdentitiesEditCommand(zone, deps))
	cmd.AddCommand(newIdentitiesDeleteCommand(zone, deps))
	cmd.AddCommand(newSendAsCommand(zone, deps))
	return cmd
}

func newIdentitiesListCommand(zone string, deps Dependencies) *cobra.Command {
	var output, selectFields string
	var filterArgs []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the addresses, how each one sends and where it receives",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			filters, err := parseFilters(filterArgs)
			if err != nil {
				return err
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			identities, err := client.ListIdentities(cmd.Context(), zone)
			if err != nil {
				return wrapAuthError(err)
			}
			identities, rows, err := applyFilters(identities, filters)
			if err != nil {
				return err
			}
			return emit(cmd.OutOrStdout(), output, parseSelect(selectFields), identities, rows,
				writeIdentitiesTable(zone), writeIdentitiesCSV(zone), "No addresses found.", true)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", outputHelp)
	addFilterAndSelectFlags(cmd, &filterArgs, &selectFields)
	return cmd
}

func printIdentity(cmd *cobra.Command, zone string, f *identityFlags, identity *api.Identity) error {
	items := []api.Identity{*identity}
	rows, err := toRowMaps(items)
	if err != nil {
		return err
	}
	return emit(cmd.OutOrStdout(), f.output, parseSelect(f.selectArgs), items, rows,
		writeIdentitiesTable(zone), writeIdentitiesCSV(zone), "", false)
}

func newIdentitiesAddCommand(zone string, deps Dependencies) *cobra.Command {
	var f identityFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an address (the part before the @)",
		Long: "Adds an address. With a password it can send; with --mailbox it receives in a mailbox (which needs a password, " +
			"the one that also opens the mailbox by IMAP); with --forward it forwards. The password is read from the " +
			"standard input or asked on the terminal, never taken as an argument.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(f.output); err != nil {
				return err
			}
			receive, err := f.receiveFrom()
			if err != nil {
				return err
			}
			if f.rename != "" || f.removePassword || f.updateForwardings {
				return errors.New("this flag is only for \"edit\"")
			}
			password, err := readPassword(cmd, deps, f.passwordStdin, f.askPassword)
			if err != nil {
				return err
			}
			if receive != nil && receive.Type == "mailbox" && password == nil {
				return errors.New("a mailbox needs a password: use --password-stdin or --ask-password")
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			name := args[0]
			enabled := f.enabled
			identity, err := client.CreateIdentity(cmd.Context(), zone, api.IdentityInput{Username: &name, Enabled: &enabled, Password: password, Receive: receive})
			if err != nil {
				return wrapAuthError(err)
			}
			return printIdentity(cmd, zone, &f, identity)
		},
	}
	addIdentityFlags(cmd, &f, false)
	return cmd
}

func newIdentitiesEditCommand(zone string, deps Dependencies) *cobra.Command {
	var f identityFlags
	cmd := &cobra.Command{
		Use:   "edit <address>",
		Short: "Change an address: rename it, enable or disable it, change its password or how it receives",
		Long: "Changes only what the flags name. <address> is its id, its name or name@zone. Changing a mailbox into a " +
			"forwarding (or --no-receive) does not delete its mail: it is kept as a deleted mailbox, which can be " +
			"recovered with \"storages attach\" until it is deleted for good.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(f.output); err != nil {
				return err
			}
			receive, err := f.receiveFrom()
			if err != nil {
				return err
			}
			if f.removePassword && (f.passwordStdin || f.askPassword) {
				return errors.New("--remove-password cannot go with a new password")
			}
			if f.updateForwardings && f.rename == "" {
				return errors.New("--update-forwardings only goes with --rename")
			}
			password, err := readPassword(cmd, deps, f.passwordStdin, f.askPassword)
			if err != nil {
				return err
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			current, err := resolveIdentity(cmd, client, zone, args[0])
			if err != nil {
				return err
			}
			input := api.IdentityInput{Password: password, RemovePassword: f.removePassword, Receive: receive, UpdateDestinations: f.updateForwardings}
			if f.rename != "" {
				input.Username = &f.rename
			}
			if cmd.Flags().Changed("enabled") {
				enabled := f.enabled
				input.Enabled = &enabled
			}
			identity, err := client.UpdateIdentity(cmd.Context(), zone, current.ID, input)
			if err != nil {
				return wrapAuthError(err)
			}
			return printIdentity(cmd, zone, &f, identity)
		},
	}
	addIdentityFlags(cmd, &f, true)
	return cmd
}

func newIdentitiesDeleteCommand(zone string, deps Dependencies) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <address>",
		Short: "Delete an address for good",
		Long: "Deletes the address: mail sent to it bounces, and its password, its forwarding and the permissions to send as " +
			"it are lost. If it has a mailbox, the mail is kept for a while as a deleted mailbox that can be recovered.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			identity, err := resolveIdentity(cmd, client, zone, args[0])
			if err != nil {
				return err
			}
			address := identity.Username + "@" + zone
			prompt := fmt.Sprintf("Delete %s? Its mail will bounce; the mail of its mailbox, if any, is kept for a while. [y/N]: ", address)
			if err := confirmOrRefuse(cmd, deps, yes, prompt, "delete"); err != nil {
				return err
			}
			if err := client.DeleteIdentity(cmd.Context(), zone, identity.ID); err != nil {
				return wrapAuthError(err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s\n", address)
			return err
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func newSendAsCommand(zone string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "send-as", Short: "Choose who can send as an address"}
	for _, action := range []struct{ use, short string }{
		{"add", "Let a credential send as an address"},
		{"remove", "Stop a credential from sending as an address"},
	} {
		add := action.use == "add"
		cmd.AddCommand(&cobra.Command{
			Use:   action.use + " <address> <credential>",
			Short: action.short,
			Long: "<credential> is an address of yours that has a password, which may be in another of your zones " +
				"(name@other-zone), or its id. <address> is the one it will send as.",
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := resolveAPI(deps)
				if err != nil {
					return err
				}
				target, err := resolveIdentity(cmd, client, zone, args[0])
				if err != nil {
					return err
				}
				credential, err := resolveCredential(cmd, client, zone, args[1])
				if err != nil {
					return err
				}
				if add {
					_, err = client.AllowSendAs(cmd.Context(), zone, target.ID, credential.ID)
				} else {
					_, err = client.RevokeSendAs(cmd.Context(), zone, target.ID, credential.ID)
				}
				if err != nil {
					return wrapAuthError(err)
				}
				verb := "can no longer send as"
				if add {
					verb = "can now send as"
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s@%s\n", args[1], verb, target.Username, zone)
				return err
			},
		})
	}
	return cmd
}

func newStoragesCommand(zone string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "storages", Short: "Recover or delete the mailboxes that were deleted"}
	cmd.AddCommand(newStoragesListCommand(zone, deps))
	cmd.AddCommand(newStoragesAttachCommand(zone, deps))
	cmd.AddCommand(newStoragesDeleteCommand(zone, deps))
	return cmd
}

func newStoragesListCommand(zone string, deps Dependencies) *cobra.Command {
	var output, selectFields string
	var filterArgs []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the deleted mailboxes that can still be recovered, with the days left",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			filters, err := parseFilters(filterArgs)
			if err != nil {
				return err
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			storages, err := client.ListStorages(cmd.Context(), zone)
			if err != nil {
				return wrapAuthError(err)
			}
			storages, rows, err := applyFilters(storages, filters)
			if err != nil {
				return err
			}
			return emit(cmd.OutOrStdout(), output, parseSelect(selectFields), storages, rows,
				writeStoragesTable, writeStoragesCSV, "No deleted mailboxes to recover.", true)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", outputHelp)
	addFilterAndSelectFlags(cmd, &filterArgs, &selectFields)
	return cmd
}

func newStoragesAttachCommand(zone string, deps Dependencies) *cobra.Command {
	var as, to string
	var f identityFlags
	cmd := &cobra.Command{
		Use:   "attach <id>",
		Short: "Recover a deleted mailbox, with its mail, into an address",
		Long: "--as NAME recovers it into a new address with that name (it needs a password, read from the standard input " +
			"or asked on the terminal). --to ADDRESS gives it to an existing address of the zone that has no mailbox and " +
			"does not forward; that is only for administrators.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(f.output); err != nil {
				return err
			}
			if (as == "") == (to == "") {
				return errors.New("say who takes it: --as NAME for a new address, or --to ADDRESS for an existing one")
			}
			password, err := readPassword(cmd, deps, f.passwordStdin, f.askPassword)
			if err != nil {
				return err
			}
			if as != "" && password == nil {
				return errors.New("a new address with a mailbox needs a password: use --password-stdin or --ask-password")
			}
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			input := api.AttachInput{Username: as}
			if password != nil {
				input.Password = *password
			}
			if cmd.Flags().Changed("enabled") {
				enabled := f.enabled
				input.Enabled = &enabled
			}
			if to != "" {
				target, err := resolveIdentity(cmd, client, zone, to)
				if err != nil {
					return err
				}
				input.IdentityID = target.ID
			}
			storage, err := resolveStorage(cmd, client, zone, args[0])
			if err != nil {
				return err
			}
			identity, err := client.AttachStorage(cmd.Context(), zone, storage.ID, input)
			if err != nil {
				return wrapAuthError(err)
			}
			return printIdentity(cmd, zone, &f, identity)
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "Name of the new address that takes it")
	cmd.Flags().StringVar(&to, "to", "", "Existing address that takes it (administrators only)")
	cmd.Flags().BoolVar(&f.enabled, "enabled", true, "Whether the new address works")
	cmd.Flags().BoolVar(&f.passwordStdin, "password-stdin", false, "Read the password from the standard input")
	cmd.Flags().BoolVar(&f.askPassword, "ask-password", false, "Ask for the password on the terminal, without echo")
	cmd.Flags().StringVarP(&f.output, "output", "o", "table", outputHelp)
	cmd.Flags().StringVar(&f.selectArgs, "select", "", selectFlagHelp)
	return cmd
}

func newStoragesDeleteCommand(zone string, deps Dependencies) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete the mail of a deleted mailbox now, without waiting",
		Long:  "Deletes the mail for good, with no way back, and frees the place it takes in your limit at once.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveAPI(deps)
			if err != nil {
				return err
			}
			storage, err := resolveStorage(cmd, client, zone, args[0])
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Delete the mail of the deleted mailbox %s (%s) now? It cannot be recovered afterwards. [y/N]: ", storage.Name, shortID(storage.ID))
			if err := confirmOrRefuse(cmd, deps, yes, prompt, "delete"); err != nil {
				return err
			}
			if err := client.DeleteStorage(cmd.Context(), zone, storage.ID); err != nil {
				return wrapAuthError(err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted the mail of %s\n", storage.Name)
			return err
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

// newRetiredCommand is what "mailboxes" and "forwardings" became: the Dymmer API no longer serves them (it answers
// "update dym"), so this says where they went instead of failing with a generic error.
func newRetiredCommand(zone, name string) *cobra.Command {
	return &cobra.Command{
		Use:    name,
		Short:  "Removed: use \"identities\"",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("%q was replaced by \"identities\": try \"dym domain %s identities list\"", name, zone)
		},
	}
}
