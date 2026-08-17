package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dymmer-code/dym/internal/api"
)

func TestWriteRecordsTableNormalRows(t *testing.T) {
	out := new(bytes.Buffer)
	records := []api.Record{
		{ID: "r1", Type: "A", Host: "www", TimeToLive: 300, Content: map[string]string{"ip": "203.0.113.10"}},
	}
	if err := writeRecordsTable(out, records); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "ID") || !strings.Contains(got, "TYPE") || !strings.Contains(got, "HOST") ||
		!strings.Contains(got, "TTL") || !strings.Contains(got, "CONTENT") {
		t.Fatalf("missing expected column headers: %q", got)
	}
	if !strings.Contains(got, "r1") || !strings.Contains(got, "A") || !strings.Contains(got, "www") ||
		!strings.Contains(got, "300") || !strings.Contains(got, "ip=203.0.113.10") {
		t.Fatalf("missing expected row content: %q", got)
	}
}

func TestWriteRecordsTableEmpty(t *testing.T) {
	out := new(bytes.Buffer)
	if err := writeRecordsTable(out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No records found.\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestWriteRecordsTableApexHostSubstitution(t *testing.T) {
	out := new(bytes.Buffer)
	records := []api.Record{{ID: "r1", Type: "A", Host: "", Content: map[string]string{"ip": "1.2.3.4"}}}
	if err := writeRecordsTable(out, records); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got %d lines: %q", len(lines), out.String())
	}
	fields := strings.Fields(lines[1])
	// fields: ID TYPE HOST TTL CONTENT...
	if len(fields) < 3 || fields[2] != "@" {
		t.Fatalf("expected HOST column to be @ for empty host, row = %q", lines[1])
	}
}

func TestWriteRecordsTableContentKeysSortedDeterministically(t *testing.T) {
	out := new(bytes.Buffer)
	// A map literal with 3+ keys — Go does not guarantee iteration order,
	// so the rendered output must still come out alphabetically sorted.
	record := api.Record{
		ID:   "r1",
		Type: "SRV",
		Host: "svc",
		Content: map[string]string{
			"weight":   "10",
			"port":     "5060",
			"priority": "0",
			"target":   "sip.example.com",
		},
	}
	if err := writeRecordsTable(out, []api.Record{record}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "port=5060, priority=0, target=sip.example.com, weight=10") {
		t.Fatalf("content not alphabetically sorted: %q", out.String())
	}
}

func TestContentStringSortedDirectly(t *testing.T) {
	content := map[string]string{"z": "1", "a": "2", "m": "3"}
	got := contentString(content)
	want := "a=2, m=3, z=1"
	if got != want {
		t.Fatalf("contentString = %q, want %q", got, want)
	}
}

func TestWriteMailboxesTableNormalRows(t *testing.T) {
	out := new(bytes.Buffer)
	mailboxes := []api.Mailbox{{Username: "alice", Enabled: true, PasswordMD5: "abc123"}}
	if err := writeMailboxesTable(out, mailboxes); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "USERNAME") || !strings.Contains(got, "ENABLED") || !strings.Contains(got, "PASSWORD_MD5") {
		t.Fatalf("missing expected column headers: %q", got)
	}
	if !strings.Contains(got, "alice") || !strings.Contains(got, "true") || !strings.Contains(got, "abc123") {
		t.Fatalf("missing expected row content: %q", got)
	}
}

func TestWriteMailboxesTableEmpty(t *testing.T) {
	out := new(bytes.Buffer)
	if err := writeMailboxesTable(out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No mailboxes found.\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestWriteForwardingsTableNormalRows(t *testing.T) {
	out := new(bytes.Buffer)
	forwardings := []api.Forwarding{{Username: "alice", Destination: []string{"bob@example.org"}, Enabled: true}}
	if err := writeForwardingsTable(out, forwardings); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "USERNAME") || !strings.Contains(got, "DESTINATION") || !strings.Contains(got, "ENABLED") {
		t.Fatalf("missing expected column headers: %q", got)
	}
	if !strings.Contains(got, "alice") || !strings.Contains(got, "bob@example.org") || !strings.Contains(got, "true") {
		t.Fatalf("missing expected row content: %q", got)
	}
}

func TestWriteForwardingsTableMultiAddressJoin(t *testing.T) {
	out := new(bytes.Buffer)
	forwardings := []api.Forwarding{
		{Username: "alice", Destination: []string{"bob@example.org", "carol@example.org"}, Enabled: false},
	}
	if err := writeForwardingsTable(out, forwardings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bob@example.org, carol@example.org") {
		t.Fatalf("destinations not joined as expected: %q", out.String())
	}
}

func TestWriteForwardingsTableEmpty(t *testing.T) {
	out := new(bytes.Buffer)
	if err := writeForwardingsTable(out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No forwardings found.\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestWriteSecretsTableNormalRows(t *testing.T) {
	out := new(bytes.Buffer)
	entries := []api.SecretEntry{{Key: "API_KEY", Value: "secret-value", Comments: "ignored", Removed: false}}
	if err := writeSecretsTable(out, entries); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "KEY") || !strings.Contains(got, "VALUE") {
		t.Fatalf("missing expected column headers: %q", got)
	}
	if !strings.Contains(got, "API_KEY") || !strings.Contains(got, "secret-value") {
		t.Fatalf("missing expected row content: %q", got)
	}
	if strings.Contains(got, "ignored") {
		t.Fatalf("table must not include Comments column: %q", got)
	}
}

func TestWriteSecretsTableEmpty(t *testing.T) {
	out := new(bytes.Buffer)
	if err := writeSecretsTable(out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No secrets found.\n" {
		t.Fatalf("got %q", out.String())
	}
}
