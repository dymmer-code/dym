package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dymmer-code/dym/internal/api"
)

// fakeAPI implements APIClient for tests.
type fakeAPI struct {
	records     []api.Record
	mailboxes   []api.Mailbox
	forwardings []api.Forwarding

	createResult *api.Record
	updateResult *api.Record
	deleteResult *api.Record

	listErr        error
	createErr      error
	updateErr      error
	deleteErr      error
	mailboxesErr   error
	forwardingsErr error

	lastListDomain  string
	lastListType    string
	lastCreateInput api.RecordInput
	lastUpdateID    string
	lastUpdateInput api.RecordInput
	deletedID       string
}

func (f *fakeAPI) ListRecords(_ context.Context, domain, recordType string) ([]api.Record, error) {
	f.lastListDomain = domain
	f.lastListType = recordType
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.records, nil
}

func (f *fakeAPI) CreateRecord(_ context.Context, _ string, input api.RecordInput) (*api.Record, error) {
	f.lastCreateInput = input
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createResult != nil {
		return f.createResult, nil
	}
	return &api.Record{ID: "new-record", Type: input.Type, Host: input.Host}, nil
}

func (f *fakeAPI) UpdateRecord(_ context.Context, _ string, id string, input api.RecordInput) (*api.Record, error) {
	f.lastUpdateID = id
	f.lastUpdateInput = input
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.updateResult != nil {
		return f.updateResult, nil
	}
	return &api.Record{ID: id, Type: input.Type, Host: input.Host}, nil
}

func (f *fakeAPI) DeleteRecord(_ context.Context, _ string, id string) (*api.Record, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deletedID = id
	if f.deleteResult != nil {
		return f.deleteResult, nil
	}
	return &api.Record{ID: id}, nil
}

func (f *fakeAPI) ListMailboxes(_ context.Context, _ string) ([]api.Mailbox, error) {
	if f.mailboxesErr != nil {
		return nil, f.mailboxesErr
	}
	return f.mailboxes, nil
}

func (f *fakeAPI) ListForwardings(_ context.Context, _ string) ([]api.Forwarding, error) {
	if f.forwardingsErr != nil {
		return nil, f.forwardingsErr
	}
	return f.forwardings, nil
}

func (f *fakeAPI) GetSecrets(_ context.Context, _, _, _, _ string) (*api.SecretsResult, error) {
	return &api.SecretsResult{}, nil
}

func terminalTrue() bool  { return true }
func terminalFalse() bool { return false }

// TestRecordDeleteDeclinedDoesNotCallAPI is the plan's own example test.
func TestRecordDeleteDeclinedDoesNotCallAPI(t *testing.T) {
	api := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{API: api, IsTerminal: func() bool { return true }, Confirm: func(string) (bool, error) { return false, nil }})
	cmd.SetArgs([]string{"domain", "example.com", "records", "delete", "record-1"})
	if err := cmd.Execute(); err == nil || api.deletedID != "" {
		t.Fatal("delete was not cancelled")
	}
}

func TestRecordDeleteConfirmedCallsAPIAndPrintsJSON(t *testing.T) {
	fake := &fakeAPI{deleteResult: &api.Record{ID: "record-1", Type: "A", Host: "www"}}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	var promptSeen string
	cmd := NewRootCommand(Dependencies{
		Out: out, Err: errOut, API: fake, IsTerminal: terminalTrue,
		Confirm: func(prompt string) (bool, error) { promptSeen = prompt; return true, nil },
	})
	cmd.SetArgs([]string{"domain", "example.com", "records", "delete", "record-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.deletedID != "record-1" {
		t.Fatalf("deletedID = %q, want record-1", fake.deletedID)
	}
	if promptSeen != "Delete DNS record record-1 from example.com? [y/N]: " {
		t.Fatalf("prompt = %q", promptSeen)
	}
	var got api.Record
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON record: %v (stdout=%q)", err, out.String())
	}
	if got.ID != "record-1" {
		t.Fatalf("decoded id = %q", got.ID)
	}
}

func TestRecordDeleteWithYesSkipsPrompt(t *testing.T) {
	fake := &fakeAPI{}
	confirmCalled := false
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake, IsTerminal: terminalTrue,
		Confirm: func(string) (bool, error) { confirmCalled = true; return false, nil },
	})
	cmd.SetArgs([]string{"domain", "example.com", "records", "delete", "record-1", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if confirmCalled {
		t.Fatal("--yes must skip the confirmation prompt")
	}
	if fake.deletedID != "record-1" {
		t.Fatal("expected DeleteRecord to be called")
	}
}

func TestRecordDeleteNonInteractiveWithoutYesFails(t *testing.T) {
	fake := &fakeAPI{}
	confirmCalled := false
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake, IsTerminal: terminalFalse,
		Confirm: func(string) (bool, error) { confirmCalled = true; return true, nil },
	})
	cmd.SetArgs([]string{"domain", "example.com", "records", "delete", "record-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for non-interactive delete without --yes")
	}
	if confirmCalled {
		t.Fatal("must not prompt in a non-interactive session")
	}
	if fake.deletedID != "" {
		t.Fatal("must not call DeleteRecord without confirmation")
	}
}

func TestRecordDeletePromptGoesToStderrNotStdout(t *testing.T) {
	fake := &fakeAPI{}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{
		Out: out, Err: errOut, API: fake, IsTerminal: terminalTrue,
		Confirm: func(prompt string) (bool, error) {
			errOut.WriteString(prompt)
			return true, nil
		},
	})
	cmd.SetArgs([]string{"domain", "example.com", "records", "delete", "record-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "Delete DNS record record-1 from example.com?") {
		t.Fatalf("prompt not on stderr: %q", errOut.String())
	}
	if strings.Contains(out.String(), "Delete DNS record") {
		t.Fatal("prompt leaked to stdout")
	}
	var got api.Record
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not clean JSON: %v (stdout=%q)", err, out.String())
	}
}

func TestRecordsListPassesTypeFilterAndEncodesJSON(t *testing.T) {
	fake := &fakeAPI{records: []api.Record{{ID: "r1", Type: "A", Host: "www", Content: map[string]string{"ip": "203.0.113.10"}}}}
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "list", "--type", "A"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastListDomain != "example.com" || fake.lastListType != "A" {
		t.Fatalf("ListRecords called with domain=%q type=%q", fake.lastListDomain, fake.lastListType)
	}
	var got []api.Record
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%q)", err, out.String())
	}
	if len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("decoded records = %+v", got)
	}
}

func TestRecordsListWithoutTypeOmitsFilter(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastListType != "" {
		t.Fatalf("lastListType = %q, want empty", fake.lastListType)
	}
}

func TestRecordsCreateMapsFlagsToInput(t *testing.T) {
	fake := &fakeAPI{}
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{
		"domain", "example.com", "records", "create",
		"--type", "A", "--name", "www", "--ttl", "300", "--ip", "203.0.113.10",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	in := fake.lastCreateInput
	if in.Type != "A" || in.Host != "www" {
		t.Fatalf("input = %+v", in)
	}
	if in.TTL == nil || *in.TTL != 300 {
		t.Fatalf("ttl = %v", in.TTL)
	}
	if in.Content["ip"] != "203.0.113.10" {
		t.Fatalf("content = %+v", in.Content)
	}
	if _, ok := in.Content["value"]; ok {
		t.Fatalf("unset content flags must be omitted: %+v", in.Content)
	}
	var got api.Record
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
}

func TestRecordsCreateOmitsUnsetTTLAndName(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "create", "--type", "TXT", "--value", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	in := fake.lastCreateInput
	if in.Host != "" {
		t.Fatalf("host = %q, want empty (omit means apex)", in.Host)
	}
	if in.TTL != nil {
		t.Fatalf("ttl = %v, want nil (unset means server default)", *in.TTL)
	}
	if in.Content["value"] != "hello" {
		t.Fatalf("content = %+v", in.Content)
	}
	if _, ok := in.Content["ip"]; ok {
		t.Fatal("ip must not be present when unset")
	}
}

func TestRecordsCreateRequiresType(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "create", "--name", "www", "--ip", "1.2.3.4"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --type is missing")
	}
	if fake.lastCreateInput.Type != "" {
		t.Fatal("must not call CreateRecord without --type")
	}
}

func TestRecordsUpdateRequiresTypeEvenIfUnchanged(t *testing.T) {
	fake := &fakeAPI{}
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "update", "record-1", "--ip", "1.2.3.4"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --type is missing on update")
	}
	if fake.lastUpdateID != "" {
		t.Fatal("must not call UpdateRecord without --type")
	}
}

func TestRecordsUpdateCallsAPIWithIDAndPrintsJSON(t *testing.T) {
	fake := &fakeAPI{}
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "update", "record-1", "--type", "A", "--ip", "9.9.9.9"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if fake.lastUpdateID != "record-1" {
		t.Fatalf("update id = %q", fake.lastUpdateID)
	}
	if fake.lastUpdateInput.Type != "A" || fake.lastUpdateInput.Content["ip"] != "9.9.9.9" {
		t.Fatalf("update input = %+v", fake.lastUpdateInput)
	}
	var got api.Record
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
}

func TestMailboxesListPrintsJSON(t *testing.T) {
	fake := &fakeAPI{mailboxes: []api.Mailbox{{Username: "alice", Enabled: true}}}
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "mailboxes", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []api.Mailbox
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%q)", err, out.String())
	}
	if len(got) != 1 || got[0].Username != "alice" {
		t.Fatalf("decoded mailboxes = %+v", got)
	}
}

func TestForwardingsListPrintsJSON(t *testing.T) {
	fake := &fakeAPI{forwardings: []api.Forwarding{{Username: "alice", Destination: []string{"bob@example.org"}, Enabled: true}}}
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "forwardings", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []api.Forwarding
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%q)", err, out.String())
	}
	if len(got) != 1 || got[0].Username != "alice" {
		t.Fatalf("decoded forwardings = %+v", got)
	}
}

func TestDomainCommandRequiresDomainArg(t *testing.T) {
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: &fakeAPI{}})
	cmd.SetArgs([]string{"domain"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when no domain is given")
	}
}

func TestListErrorPropagatesAndProducesNoStdout(t *testing.T) {
	fake := &fakeAPI{listErr: errors.New("boom")}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: errOut, API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "list"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error to propagate")
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", out.String())
	}
}

func TestUnauthorizedErrorSuggestsLogin(t *testing.T) {
	fake := &fakeAPI{listErr: &api.APIError{StatusCode: 401, Reason: "authentication failed"}}
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "records", "list"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "dym auth login") || !strings.Contains(err.Error(), "DYMMER_TOKEN") {
		t.Fatalf("error = %q, want guidance to run dym auth login or set DYMMER_TOKEN", err.Error())
	}
}

func TestNoAPIAndNoCredentialsReturnsActionableError(t *testing.T) {
	store := &fakeStore{err: errors.New("keychain unavailable")}
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer),
		Store: store, Env: func(string) string { return "" },
	})
	cmd.SetArgs([]string{"domain", "example.com", "records", "list"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no credentials are available")
	}
	if strings.Contains(err.Error(), "keychain unavailable") {
		t.Fatalf("must not echo underlying keychain error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "dym auth login") {
		t.Fatalf("error = %q, want actionable guidance", err.Error())
	}
}
