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

const (
	idBoss   = "11111111-1111-1111-1111-111111111111"
	idInfo   = "22222222-2222-2222-2222-222222222222"
	idSender = "33333333-3333-3333-3333-333333333333"
	idAbroad = "44444444-4444-4444-4444-444444444444"
)

func (f *fakeAPI) mail(call string) error {
	f.mailCalls = append(f.mailCalls, call)
	return f.mailErr
}

func (f *fakeAPI) first() *api.Identity { return &f.identities[0] }

func (f *fakeAPI) ListIdentities(_ context.Context, zone string) ([]api.Identity, error) {
	f.mailCalls = append(f.mailCalls, "list "+zone)
	if f.byZone != nil {
		return f.byZone[zone], f.mailErr
	}
	return f.identities, f.mailErr
}

func (f *fakeAPI) CreateIdentity(_ context.Context, zone string, input api.IdentityInput) (*api.Identity, error) {
	f.mailInput = input
	if err := f.mail("create " + zone); err != nil {
		return nil, err
	}
	return &api.Identity{ID: "new", Username: *input.Username, Enabled: true}, nil
}

func (f *fakeAPI) UpdateIdentity(_ context.Context, zone, id string, input api.IdentityInput) (*api.Identity, error) {
	f.mailInput = input
	if err := f.mail("update " + zone + " " + id); err != nil {
		return nil, err
	}
	return &api.Identity{ID: id, Username: "boss", Enabled: true}, nil
}

func (f *fakeAPI) DeleteIdentity(_ context.Context, zone, id string) error {
	return f.mail("delete " + zone + " " + id)
}

func (f *fakeAPI) AllowSendAs(_ context.Context, zone, id, credential string) (*api.Identity, error) {
	return &api.Identity{ID: id}, f.mail("allow " + zone + " " + id + " " + credential)
}

func (f *fakeAPI) RevokeSendAs(_ context.Context, zone, id, credential string) (*api.Identity, error) {
	return &api.Identity{ID: id}, f.mail("revoke " + zone + " " + id + " " + credential)
}

func (f *fakeAPI) ListStorages(_ context.Context, zone string) ([]api.Storage, error) {
	f.mailCalls = append(f.mailCalls, "storages "+zone)
	return f.storages, f.mailErr
}

func (f *fakeAPI) AttachStorage(_ context.Context, zone, id string, input api.AttachInput) (*api.Identity, error) {
	f.attached = input
	if err := f.mail("attach " + zone + " " + id); err != nil {
		return nil, err
	}
	return &api.Identity{ID: "n", Username: "again", Enabled: true, HasPassword: true, Mailbox: &api.IdentityMailbox{QuotaTier: "5gb", StorageKey: "k"}}, nil
}

func (f *fakeAPI) DeleteStorage(_ context.Context, zone, id string) error {
	return f.mail("purge " + zone + " " + id)
}

func zoneIdentities() []api.Identity {
	return []api.Identity{
		{ID: idBoss, Username: "boss", Enabled: true, HasPassword: true, Authorizations: []string{"boss@example.com"},
			Mailbox: &api.IdentityMailbox{QuotaTier: "5gb", QuotaUsedMB: 12, StorageKey: "k"}},
		{ID: idInfo, Username: "info", Enabled: false, Authorizations: []string{"sender@example.com"},
			Forwarding: &api.IdentityForwarding{Destination: []string{"a@x.com", "b@x.com"}}},
		{ID: idSender, Username: "sender", Enabled: true, HasPassword: true, Authorizations: []string{"sender@example.com"}},
		{ID: "55555555-5555-5555-5555-555555555555", Username: "ghost", Enabled: true},
	}
}

type runOptions struct {
	stdin    string
	secrets  []string
	terminal bool
	confirm  *bool
}

func runMail(t *testing.T, fake *fakeAPI, opts runOptions, args ...string) (stdout string, err error) {
	t.Helper()
	out := new(bytes.Buffer)
	secrets := opts.secrets
	deps := Dependencies{
		Out: out, Err: new(bytes.Buffer), API: fake,
		IsTerminal: func() bool { return opts.terminal },
		ReadSecret: func(string) (string, error) {
			if len(secrets) == 0 {
				return "", errors.New("no secret left")
			}
			secret := secrets[0]
			secrets = secrets[1:]
			return secret, nil
		},
	}
	if opts.confirm != nil {
		deps.Confirm = func(string) (bool, error) { return *opts.confirm, nil }
	}
	cmd := NewRootCommand(deps)
	cmd.SetIn(strings.NewReader(opts.stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

func TestIdentitiesListShowsHowEachAddressSendsAndReceives(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	out, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "list")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"ADDRESS", "boss@example.com", "credential", "mailbox, 12 MB of 5GB", "info@example.com", "forwards to a@x.com, b@x.com", "sender@example.com"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	var ghost string
	for _, line := range lines {
		if strings.Contains(line, "ghost@") {
			ghost = line
		}
	}
	if strings.Count(ghost, " -") < 2 {
		t.Fatalf("an address that neither sends nor receives shows dashes: %q", ghost)
	}
	if strings.Contains(out, "SHA512") || strings.Contains(out, "hash") {
		t.Fatalf("no hash: %s", out)
	}
}

func TestIdentitiesListJSONKeepsTheFieldsOfTheAPIWithoutTheHash(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	out, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err, out)
	}
	if len(got) != 4 || got[0]["id"] != idBoss || got[0]["has_password"] != true || got[0]["password_hash"] != nil {
		t.Fatalf("%v", got[0])
	}
}

func TestIdentitiesListCanFilterAndSelect(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	out, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "list", "--filter", "enabled=false", "--select", "id,username", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, idInfo) || strings.Contains(out, idBoss) {
		t.Fatalf("%s", out)
	}
}

func TestIdentitiesAddPasswordComesFromTheStandardInputAndNeverFromAnArgument(t *testing.T) {
	fake := &fakeAPI{}

	out, err := runMail(t, fake, runOptions{stdin: "long-enough-1\n"}, "domain", "example.com", "identities", "add", "sales", "--password-stdin", "--mailbox")
	if err != nil {
		t.Fatal(err)
	}

	if fake.mailCalls[0] != "create example.com" {
		t.Fatalf("%v", fake.mailCalls)
	}
	in := fake.mailInput
	if *in.Username != "sales" || *in.Password != "long-enough-1" || in.Receive.Type != "mailbox" || !*in.Enabled {
		t.Fatalf("%+v %+v", in, in.Receive)
	}
	if strings.Contains(out, "long-enough-1") {
		t.Fatalf("the password is never printed: %s", out)
	}

	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), API: fake})
	cmd.SetArgs([]string{"domain", "example.com", "identities", "add", "x", "--password", "secret"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("there is no --password flag: it would be seen in the list of processes")
	}
}

func TestIdentitiesAddAsksTwiceOnTheTerminalAndTheyMustMatch(t *testing.T) {
	fake := &fakeAPI{}

	if _, err := runMail(t, fake, runOptions{secrets: []string{"one-password-1", "other-password-2"}}, "domain", "example.com", "identities", "add", "sales", "--ask-password"); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("%v", err)
	}
	if len(fake.mailCalls) != 0 {
		t.Fatalf("nothing is sent: %v", fake.mailCalls)
	}

	if _, err := runMail(t, fake, runOptions{secrets: []string{"same-password-1", "same-password-1"}}, "domain", "example.com", "identities", "add", "sales", "--ask-password"); err != nil {
		t.Fatal(err)
	}
	if *fake.mailInput.Password != "same-password-1" {
		t.Fatalf("%+v", fake.mailInput)
	}
}

func TestIdentitiesAddRefusesWhatCannotBeBeforeAskingTheAPI(t *testing.T) {
	for name, args := range map[string][]string{
		"mailbox and forward":      {"--mailbox", "--forward", "a@x.com", "--password-stdin"},
		"mailbox without password": {"--mailbox"},
		"storage without mailbox":  {"--from-storage", "abc"},
		"edit flag on add":         {"--rename", "other"},
		"two ways to the password": {"--password-stdin", "--ask-password"},
	} {
		fake := &fakeAPI{}
		_, err := runMail(t, fake, runOptions{stdin: "long-enough-1\n"}, append([]string{"domain", "example.com", "identities", "add", "sales"}, args...)...)
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if len(fake.mailCalls) != 0 {
			t.Fatalf("%s: the API must not be called: %v", name, fake.mailCalls)
		}
	}
}

func TestIdentitiesAddForwardsToSeveralAddresses(t *testing.T) {
	fake := &fakeAPI{}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "add", "info", "--forward", "a@x.com,b@x.com", "--forward", "c@x.com", "--enabled=false"); err != nil {
		t.Fatal(err)
	}

	in := fake.mailInput
	if in.Receive.Type != "forwarding" || strings.Join(in.Receive.Destination, ",") != "a@x.com,b@x.com,c@x.com" || *in.Enabled || in.Password != nil {
		t.Fatalf("%+v %+v", in, in.Receive)
	}
}

func TestIdentitiesEditRenamesAndFindsTheAddressByIdByNameOrByAddress(t *testing.T) {
	for _, ref := range []string{idBoss, "boss", "boss@example.com", "BOSS@Example.com"} {
		fake := &fakeAPI{identities: zoneIdentities()}

		if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "edit", ref, "--rename", "marta", "--update-forwardings"); err != nil {
			t.Fatalf("%s: %v", ref, err)
		}

		if fake.mailCalls[len(fake.mailCalls)-1] != "update example.com "+idBoss {
			t.Fatalf("%s: %v", ref, fake.mailCalls)
		}
		in := fake.mailInput
		if *in.Username != "marta" || !in.UpdateDestinations || in.Enabled != nil || in.Receive != nil || in.Password != nil {
			t.Fatalf("only what was named is sent: %+v", in)
		}
	}
}

func TestIdentitiesEditOnlyTouchesWhatIsNamed(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "edit", "boss", "--enabled=false"); err != nil {
		t.Fatal(err)
	}
	if in := fake.mailInput; in.Enabled == nil || *in.Enabled || in.Username != nil || in.Receive != nil || in.RemovePassword {
		t.Fatalf("%+v", in)
	}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "edit", "boss", "--remove-password", "--no-receive"); err != nil {
		t.Fatal(err)
	}
	if in := fake.mailInput; !in.RemovePassword || in.Receive.Type != "none" || in.Enabled != nil {
		t.Fatalf("%+v", in)
	}
}

func TestIdentitiesEditRefusesWhatDoesNotMakeSense(t *testing.T) {
	for name, args := range map[string][]string{
		"remove and set the password": {"--remove-password", "--password-stdin"},
		"update without rename":       {"--update-forwardings"},
		"mailbox and no receive":      {"--mailbox", "--no-receive"},
	} {
		fake := &fakeAPI{identities: zoneIdentities()}
		if _, err := runMail(t, fake, runOptions{stdin: "long-enough-1\n"}, append([]string{"domain", "example.com", "identities", "edit", "boss"}, args...)...); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		for _, call := range fake.mailCalls {
			if strings.HasPrefix(call, "update") {
				t.Fatalf("%s: must not change anything", name)
			}
		}
	}
}

func TestIdentitiesEditUnknownAddressSaysSo(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	_, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "edit", "nobody", "--enabled=false")

	if err == nil || !strings.Contains(err.Error(), `no address "nobody"`) {
		t.Fatalf("%v", err)
	}
}

func TestIdentitiesDeleteAsksForConfirmation(t *testing.T) {
	no, yes := false, true
	fake := &fakeAPI{identities: zoneIdentities()}

	if _, err := runMail(t, fake, runOptions{terminal: true, confirm: &no}, "domain", "example.com", "identities", "delete", "boss"); err == nil {
		t.Fatal("a refusal is an error")
	}
	for _, call := range fake.mailCalls {
		if strings.HasPrefix(call, "delete") {
			t.Fatal("deleted without confirmation")
		}
	}

	out, err := runMail(t, fake, runOptions{terminal: true, confirm: &yes}, "domain", "example.com", "identities", "delete", "boss")
	if err != nil || !strings.Contains(out, "Deleted boss@example.com") {
		t.Fatalf("%q %v", out, err)
	}
	if fake.mailCalls[len(fake.mailCalls)-1] != "delete example.com "+idBoss {
		t.Fatalf("%v", fake.mailCalls)
	}
}

func TestIdentitiesDeleteWithoutATerminalNeedsYes(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "delete", "boss"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("%v", err)
	}
	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "delete", "boss", "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestSendAsFindsTheCredentialInThisZoneOrInAnotherOfYours(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "send-as", "add", "info", "sender@example.com"); err != nil {
		t.Fatal(err)
	}
	if fake.mailCalls[len(fake.mailCalls)-1] != "allow example.com "+idInfo+" "+idSender {
		t.Fatalf("%v", fake.mailCalls)
	}

	fake = &fakeAPI{byZone: map[string][]api.Identity{
		"example.com": zoneIdentities(),
		"other.org":   {{ID: idAbroad, Username: "abroad", HasPassword: true}},
	}}
	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "send-as", "add", "info", "abroad@other.org"); err != nil {
		t.Fatal(err)
	}
	if fake.mailCalls[len(fake.mailCalls)-2] != "list other.org" || fake.mailCalls[len(fake.mailCalls)-1] != "allow example.com "+idInfo+" "+idAbroad {
		t.Fatalf("the zone of the credential is asked for: %v", fake.mailCalls)
	}
}

func TestSendAsRefusesACredentialWithoutPasswordAndPassesAnIdAsItIs(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities()}

	_, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "send-as", "add", "info", "ghost")
	if err == nil || !strings.Contains(err.Error(), "has no password") {
		t.Fatalf("%v", err)
	}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "send-as", "remove", "info", idAbroad); err != nil {
		t.Fatal(err)
	}
	if fake.mailCalls[len(fake.mailCalls)-1] != "revoke example.com "+idInfo+" "+idAbroad {
		t.Fatalf("%v", fake.mailCalls)
	}
}

func TestStoragesList(t *testing.T) {
	size := int64(734003200)
	fake := &fakeAPI{storages: []api.Storage{
		{ID: "s1", Name: "old", AbandonedAt: "2026-11-02T10:00:00Z", DaysLeft: 27, UsageBytes: &size},
		{ID: "s2", Name: "archive", AbandonedAt: "2026-10-16T10:00:00Z", DaysLeft: 10},
	}}

	out, err := runMail(t, fake, runOptions{}, "domain", "example.com", "storages", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DAYS LEFT", "old", "700 MB", "27", "archive", "unknown"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}

	out, err = runMail(t, &fakeAPI{}, runOptions{}, "domain", "example.com", "storages", "list")
	if err != nil || !strings.Contains(out, "No deleted mailboxes") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestStoragesAttachToANewAddress(t *testing.T) {
	fake := &fakeAPI{storages: []api.Storage{{ID: "s1", Name: "old"}}}

	out, err := runMail(t, fake, runOptions{stdin: "long-enough-1\n"}, "domain", "example.com", "storages", "attach", "s1", "--as", "again", "--password-stdin")
	if err != nil {
		t.Fatal(err)
	}
	if fake.mailCalls[len(fake.mailCalls)-1] != "attach example.com s1" || fake.attached.Username != "again" || fake.attached.Password != "long-enough-1" {
		t.Fatalf("%v %+v", fake.mailCalls, fake.attached)
	}
	if !strings.Contains(out, "again@example.com") || strings.Contains(out, "long-enough-1") {
		t.Fatalf("%s", out)
	}
}

func TestStoragesAttachToAnExistingAddressNeedsNoPassword(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities(), storages: []api.Storage{{ID: "s1", Name: "old"}}}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "storages", "attach", "s1", "--to", "ghost"); err != nil {
		t.Fatal(err)
	}
	if fake.attached.IdentityID != "55555555-5555-5555-5555-555555555555" || fake.attached.Username != "" {
		t.Fatalf("%+v", fake.attached)
	}
}

func TestStoragesAttachNeedsToKnowWhoTakesIt(t *testing.T) {
	for name, args := range map[string][]string{
		"nobody":               {},
		"both":                 {"--as", "a", "--to", "b", "--password-stdin"},
		"new without password": {"--as", "again"},
	} {
		fake := &fakeAPI{identities: zoneIdentities()}
		if _, err := runMail(t, fake, runOptions{stdin: "long-enough-1\n"}, append([]string{"domain", "example.com", "storages", "attach", "s1"}, args...)...); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		for _, call := range fake.mailCalls {
			if strings.HasPrefix(call, "attach") {
				t.Fatalf("%s: must not ask the API", name)
			}
		}
	}
}

func TestStoragesDeleteIsForGoodSoItAsks(t *testing.T) {
	fake := &fakeAPI{storages: []api.Storage{{ID: "s1", Name: "old"}}}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "storages", "delete", "s1"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("%v", err)
	}
	for _, call := range fake.mailCalls {
		if strings.HasPrefix(call, "purge") {
			t.Fatalf("%v", fake.mailCalls)
		}
	}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "storages", "delete", "s1", "--yes"); err != nil {
		t.Fatal(err)
	}
	if fake.mailCalls[len(fake.mailCalls)-1] != "purge example.com s1" {
		t.Fatalf("%v", fake.mailCalls)
	}
}

func TestAnIdIsEnoughByItsBeginning(t *testing.T) {
	storages := []api.Storage{
		{ID: "3f2a9c10-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Name: "one"},
		{ID: "3f7b0000-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Name: "two"},
	}

	fake := &fakeAPI{storages: storages}
	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "storages", "delete", "3f2", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := fake.mailCalls[len(fake.mailCalls)-1]; got != "purge example.com "+storages[0].ID {
		t.Fatalf("%s", got)
	}

	fake = &fakeAPI{storages: storages}
	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "storages", "delete", "3f", "--yes"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("%v", err)
	}
	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "storages", "delete", "zzz", "--yes"); err == nil {
		t.Fatal("expected an error for an unknown id")
	}

	out, err := runMail(t, &fakeAPI{storages: storages}, runOptions{}, "domain", "example.com", "storages", "list")
	if err != nil || !strings.Contains(out, "3f2a9c10") || strings.Contains(out, "3f2a9c10-aaaa") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestAnIdentityIsFoundByTheBeginningOfItsId(t *testing.T) {
	fake := &fakeAPI{identities: []api.Identity{{ID: "9d8c7b6a-0000-4000-8000-000000000001", Username: "sales"}}}

	if _, err := runMail(t, fake, runOptions{}, "domain", "example.com", "identities", "delete", "9d8c", "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestMailboxesAndForwardingsPointToIdentities(t *testing.T) {
	for _, name := range []string{"mailboxes", "forwardings"} {
		fake := &fakeAPI{}
		_, err := runMail(t, fake, runOptions{}, "domain", "example.com", name, "list")
		if err == nil || !strings.Contains(err.Error(), "identities list") {
			t.Fatalf("%s: %v", name, err)
		}
		if len(fake.mailCalls) != 0 {
			t.Fatalf("%s: the API no longer answers them, so it is not asked", name)
		}
	}
}

func TestAnAPIErrorComesOutAsIs(t *testing.T) {
	fake := &fakeAPI{identities: zoneIdentities(), mailErr: &api.APIError{StatusCode: 409, Reason: "no free mailbox slots left -- buy more first"}}

	_, err := runMail(t, fake, runOptions{stdin: "long-enough-1\n"}, "domain", "example.com", "identities", "add", "x1x1", "--mailbox", "--password-stdin")

	if err == nil || !strings.Contains(err.Error(), "no free mailbox slots left") {
		t.Fatalf("%v", err)
	}
}
