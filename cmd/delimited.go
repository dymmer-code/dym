package cmd

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/dymmer-code/dym/internal/api"
)

// writeDelimitedRows writes rows (each already rendered as a slice of
// string cells, in column order) via encoding/csv, using comma as the
// field delimiter (',' for --output csv, '\t' for --output tsv). There is
// no header row and no trailing message: a nil/empty rows slice writes
// nothing at all. encoding/csv handles RFC 4180 quoting/escaping for cells
// containing the delimiter, a double quote, or a newline automatically.
// UseCRLF is left false so lines end in "\n", matching the rest of this
// CLI's output.
func writeDelimitedRows(w io.Writer, rows [][]string, comma rune) error {
	cw := csv.NewWriter(w)
	cw.Comma = comma
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// writeRecordsCSV/TSV renders records with the same columns, order, and
// value-formatting as writeRecordsTable (ID, TYPE, HOST, TTL, CONTENT; HOST
// "@" for the apex; CONTENT via contentString), but with no header row and
// no "No records found." message on an empty slice.
func writeRecordsCSV(w io.Writer, records []api.Record, comma rune) error {
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		host := r.Host
		if host == "" {
			host = "@"
		}
		rows = append(rows, []string{r.ID, r.Type, host, fmt.Sprintf("%d", r.TimeToLive), contentString(r.Content)})
	}
	return writeDelimitedRows(w, rows, comma)
}

// writeMailboxesCSV renders mailboxes with the same columns as
// writeMailboxesTable (USERNAME, ENABLED, PASSWORD_MD5), but with no header
// row and no "No mailboxes found." message on an empty slice.
func writeMailboxesCSV(w io.Writer, mailboxes []api.Mailbox, comma rune) error {
	rows := make([][]string, 0, len(mailboxes))
	for _, m := range mailboxes {
		rows = append(rows, []string{m.Username, fmt.Sprintf("%t", m.Enabled), m.PasswordMD5})
	}
	return writeDelimitedRows(w, rows, comma)
}

// writeForwardingsCSV renders forwardings with the same columns as
// writeForwardingsTable (USERNAME, DESTINATION joined with ", ", ENABLED),
// but with no header row and no "No forwardings found." message on an
// empty slice.
func writeForwardingsCSV(w io.Writer, forwardings []api.Forwarding, comma rune) error {
	rows := make([][]string, 0, len(forwardings))
	for _, f := range forwardings {
		rows = append(rows, []string{f.Username, strings.Join(f.Destination, ", "), fmt.Sprintf("%t", f.Enabled)})
	}
	return writeDelimitedRows(w, rows, comma)
}

// writeSecretsCSV renders secret entries with the same columns as
// writeSecretsTable (KEY, VALUE only), but with no header row and no "No
// secrets found." message on an empty slice.
func writeSecretsCSV(w io.Writer, entries []api.SecretEntry, comma rune) error {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.Key, e.Value})
	}
	return writeDelimitedRows(w, rows, comma)
}

// writeSelectedDelimited renders rows restricted to fields, in the given
// order, resolving each field the same way writeSelectedTable does
// (getPath(row, f), fmt.Sprint(v) if resolved and non-nil, empty string
// otherwise), but with no header row and no empty-message on an empty rows
// slice.
func writeSelectedDelimited(w io.Writer, rows []map[string]any, fields []string, comma rune) error {
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		values := make([]string, len(fields))
		for i, f := range fields {
			if v, ok := getPath(row, f); ok && v != nil {
				values[i] = fmt.Sprint(v)
			}
		}
		out = append(out, values)
	}
	return writeDelimitedRows(w, out, comma)
}
