package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/dymmer-code/dym/internal/api"
)

// newTabWriter returns a tabwriter configured the same way for every table
// rendered by this file: tab-separated columns with a 2-space minimum pad.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// contentString renders a record's Content map as "key=value" pairs joined
// by ", ", with keys sorted alphabetically so output is deterministic
// despite Go's randomized map iteration order.
func contentString(content map[string]string) string {
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+content[k])
	}
	return strings.Join(pairs, ", ")
}

// writeRecordsTable renders records as a human-readable table with columns
// ID, TYPE, HOST, TTL, CONTENT. An empty slice prints "No records found."
// instead of a header-only table.
func writeRecordsTable(w io.Writer, records []api.Record) error {
	if len(records) == 0 {
		_, err := fmt.Fprintln(w, "No records found.")
		return err
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tTYPE\tHOST\tTTL\tCONTENT")
	for _, r := range records {
		host := r.Host
		if host == "" {
			host = "@"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", r.ID, r.Type, host, r.TimeToLive, contentString(r.Content))
	}
	return tw.Flush()
}

// writeMailboxesTable renders mailboxes as a human-readable table with
// columns USERNAME, ENABLED, PASSWORD_MD5. An empty slice prints
// "No mailboxes found." instead of a header-only table.
func writeMailboxesTable(w io.Writer, mailboxes []api.Mailbox) error {
	if len(mailboxes) == 0 {
		_, err := fmt.Fprintln(w, "No mailboxes found.")
		return err
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "USERNAME\tENABLED\tPASSWORD_MD5")
	for _, m := range mailboxes {
		fmt.Fprintf(tw, "%s\t%t\t%s\n", m.Username, m.Enabled, m.PasswordMD5)
	}
	return tw.Flush()
}

// writeForwardingsTable renders forwardings as a human-readable table with
// columns USERNAME, DESTINATION, ENABLED. DESTINATION is joined with ", ".
// An empty slice prints "No forwardings found." instead of a header-only
// table.
func writeForwardingsTable(w io.Writer, forwardings []api.Forwarding) error {
	if len(forwardings) == 0 {
		_, err := fmt.Fprintln(w, "No forwardings found.")
		return err
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "USERNAME\tDESTINATION\tENABLED")
	for _, f := range forwardings {
		fmt.Fprintf(tw, "%s\t%s\t%t\n", f.Username, strings.Join(f.Destination, ", "), f.Enabled)
	}
	return tw.Flush()
}

// writeSecretsTable renders secret entries as a human-readable table with
// columns KEY, VALUE only (no Comments/Removed). An empty slice prints
// "No secrets found." instead of a header-only table.
func writeSecretsTable(w io.Writer, entries []api.SecretEntry) error {
	if len(entries) == 0 {
		_, err := fmt.Fprintln(w, "No secrets found.")
		return err
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "KEY\tVALUE")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\n", e.Key, e.Value)
	}
	return tw.Flush()
}
