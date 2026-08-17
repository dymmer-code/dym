package cmd

import (
	"bytes"
	"testing"

	"github.com/dymmer-code/dym/internal/api"
)

func TestWriteRecordsCSVNormalOutput(t *testing.T) {
	records := []api.Record{
		{ID: "r1", Type: "A", Host: "www", TimeToLive: 3600, Content: map[string]string{"ip": "203.0.113.10"}},
	}
	var buf bytes.Buffer
	if err := writeRecordsCSV(&buf, records, ','); err != nil {
		t.Fatal(err)
	}
	want := "r1,A,www,3600,ip=203.0.113.10\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteRecordsCSVApexHostRendersAt(t *testing.T) {
	records := []api.Record{{ID: "r1", Type: "A", Host: "", TimeToLive: 60, Content: map[string]string{"ip": "203.0.113.10"}}}
	var buf bytes.Buffer
	if err := writeRecordsCSV(&buf, records, ','); err != nil {
		t.Fatal(err)
	}
	want := "r1,A,@,60,ip=203.0.113.10\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteRecordsCSVEmptySliceWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRecordsCSV(&buf, nil, ','); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "" {
		t.Fatalf("got %q, want empty output (no header, no message)", buf.String())
	}
}

func TestWriteRecordsCSVTSVComma(t *testing.T) {
	records := []api.Record{{ID: "r1", Type: "A", Host: "www", TimeToLive: 300, Content: map[string]string{"ip": "203.0.113.10"}}}
	var buf bytes.Buffer
	if err := writeRecordsCSV(&buf, records, '\t'); err != nil {
		t.Fatal(err)
	}
	want := "r1\tA\twww\t300\tip=203.0.113.10\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteRecordsCSVQuotesValuesContainingComma(t *testing.T) {
	records := []api.Record{{ID: "r1", Type: "TXT", Host: "", TimeToLive: 0, Content: map[string]string{"value": "a,b\"c\nd"}}}
	var buf bytes.Buffer
	if err := writeRecordsCSV(&buf, records, ','); err != nil {
		t.Fatal(err)
	}
	want := "r1,TXT,@,0,\"value=a,b\"\"c\nd\"\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteMailboxesCSVNormalOutput(t *testing.T) {
	mailboxes := []api.Mailbox{{Username: "alice", Enabled: true, PasswordMD5: "abc123"}}
	var buf bytes.Buffer
	if err := writeMailboxesCSV(&buf, mailboxes, ','); err != nil {
		t.Fatal(err)
	}
	want := "alice,true,abc123\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteMailboxesCSVEmptySliceWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMailboxesCSV(&buf, nil, ','); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "" {
		t.Fatalf("got %q, want empty output", buf.String())
	}
}

func TestWriteForwardingsCSVNormalOutput(t *testing.T) {
	forwardings := []api.Forwarding{{Username: "alice", Destination: []string{"bob@example.com", "carol@example.com"}, Enabled: true}}
	var buf bytes.Buffer
	if err := writeForwardingsCSV(&buf, forwardings, ','); err != nil {
		t.Fatal(err)
	}
	want := "alice,\"bob@example.com, carol@example.com\",true\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteForwardingsCSVEmptySliceWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := writeForwardingsCSV(&buf, nil, ','); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "" {
		t.Fatalf("got %q, want empty output", buf.String())
	}
}

func TestWriteSecretsCSVNormalOutput(t *testing.T) {
	entries := []api.SecretEntry{{Key: "API_KEY", Value: "s3cr3t"}}
	var buf bytes.Buffer
	if err := writeSecretsCSV(&buf, entries, ','); err != nil {
		t.Fatal(err)
	}
	want := "API_KEY,s3cr3t\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteSecretsCSVTSVComma(t *testing.T) {
	entries := []api.SecretEntry{{Key: "API_KEY", Value: "s3cr3t"}, {Key: "OTHER", Value: "val"}}
	var buf bytes.Buffer
	if err := writeSecretsCSV(&buf, entries, '\t'); err != nil {
		t.Fatal(err)
	}
	want := "API_KEY\ts3cr3t\nOTHER\tval\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteSecretsCSVEmptySliceWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSecretsCSV(&buf, nil, ','); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "" {
		t.Fatalf("got %q, want empty output", buf.String())
	}
}

func TestWriteSelectedDelimitedSubsetWithMissingFieldAndDottedPath(t *testing.T) {
	rows := []map[string]any{
		{
			"id":      "r1",
			"type":    "A",
			"content": map[string]any{"ip": "203.0.113.10"},
		},
	}
	var buf bytes.Buffer
	if err := writeSelectedDelimited(&buf, rows, []string{"id", "missing", "content.ip"}, ','); err != nil {
		t.Fatal(err)
	}
	want := "r1,,203.0.113.10\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteSelectedDelimitedEmptyRowsWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSelectedDelimited(&buf, nil, []string{"id"}, ','); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "" {
		t.Fatalf("got %q, want empty output", buf.String())
	}
}

func TestWriteSelectedDelimitedTSVComma(t *testing.T) {
	rows := []map[string]any{
		{"id": "r1", "type": "A"},
		{"id": "r2", "type": "CNAME"},
	}
	var buf bytes.Buffer
	if err := writeSelectedDelimited(&buf, rows, []string{"id", "type"}, '\t'); err != nil {
		t.Fatal(err)
	}
	want := "r1\tA\nr2\tCNAME\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}
