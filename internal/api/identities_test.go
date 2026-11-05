package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any)) *Client {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("body %s: %v", raw, err)
			}
		}
		handler(w, r, body)
	}))
	t.Cleanup(s.Close)
	return NewClient(s.URL, "tok", s.Client())
}

func TestListIdentitiesKeepsWhetherThereIsAPasswordButNotTheHash(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		if r.Method != http.MethodGet || r.URL.Path != "/zones/example.com/identities" {
			t.Fatal(r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok","identities":[
			{"id":"a","username":"boss","enabled":true,"password_hash":"{SHA512-CRYPT}$6$x","mailbox":{"quota_tier":"5gb","quota_used_mb":3,"storage_key":"k"},"forwarding":null,"authorizations":["boss@example.com"]},
			{"id":"b","username":"info","enabled":false,"password_hash":null,"mailbox":null,"forwarding":{"destination":["a@x.com"]},"authorizations":[]}]}`)
	})

	identities, err := c.ListIdentities(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 2 || !identities[0].HasPassword || identities[1].HasPassword {
		t.Fatalf("%+v", identities)
	}
	if identities[0].Mailbox == nil || identities[0].Mailbox.QuotaUsedMB != 3 || identities[0].Forwarding != nil {
		t.Fatalf("%+v", identities[0])
	}
	if identities[1].Forwarding == nil || identities[1].Forwarding.Destination[0] != "a@x.com" {
		t.Fatalf("%+v", identities[1])
	}

	out, _ := json.Marshal(identities[0])
	if strings.Contains(string(out), "SHA512") || strings.Contains(string(out), "password_hash") {
		t.Fatalf("the hash must never come out: %s", out)
	}
}

func TestListIdentitiesNormalizesAbsentToEmptySlice(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		io.WriteString(w, `{"status":"ok"}`)
	})
	identities, err := c.ListIdentities(context.Background(), "example.com")
	if err != nil || identities == nil || len(identities) != 0 {
		t.Fatalf("%v %v", identities, err)
	}
}

func TestCreateIdentitySendsOnlyWhatIsSet(t *testing.T) {
	name, pass := "sales", "long-enough-1"
	c := serve(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if r.Method != http.MethodPost || r.URL.Path != "/zones/example.com/identities" {
			t.Fatal(r.Method, r.URL.Path)
		}
		identity := body["identity"].(map[string]any)
		if identity["username"] != "sales" || identity["password"] != "long-enough-1" {
			t.Fatalf("%v", identity)
		}
		receive := identity["receive"].(map[string]any)
		if receive["type"] != "mailbox" || receive["storage_id"] != "st-1" || receive["destination"] != nil {
			t.Fatalf("%v", receive)
		}
		if _, there := identity["enabled"]; there {
			t.Fatalf("enabled was not set: %v", identity)
		}
		io.WriteString(w, `{"status":"ok","identity":{"id":"n1","username":"sales","enabled":true,"password_hash":"h","mailbox":null,"forwarding":null,"authorizations":[]}}`)
	})

	got, err := c.CreateIdentity(context.Background(), "example.com", IdentityInput{
		Username: &name, Password: &pass, Receive: &Receive{Type: "mailbox", StorageID: "st-1"},
	})
	if err != nil || got.ID != "n1" || !got.HasPassword {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestUpdateIdentityCanRemoveThePasswordAndRename(t *testing.T) {
	name := "marta"
	c := serve(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if r.Method != http.MethodPatch || r.URL.Path != "/zones/example.com/identities/id-1" {
			t.Fatal(r.Method, r.URL.Path)
		}
		identity := body["identity"].(map[string]any)
		if value, there := identity["password"]; !there || value != nil {
			t.Fatalf("a null password was expected: %v", identity)
		}
		if identity["username"] != "marta" || identity["update_destinations"] != true {
			t.Fatalf("%v", identity)
		}
		receive := identity["receive"].(map[string]any)
		destination := receive["destination"].([]any)
		if receive["type"] != "forwarding" || len(destination) != 1 || destination[0] != "a@x.com" {
			t.Fatalf("%v", receive)
		}
		io.WriteString(w, `{"status":"ok","identity":{"id":"id-1","username":"marta","enabled":true,"password_hash":null,"mailbox":null,"forwarding":{"destination":["a@x.com"]},"authorizations":[]}}`)
	})

	got, err := c.UpdateIdentity(context.Background(), "example.com", "id-1", IdentityInput{
		Username: &name, RemovePassword: true, UpdateDestinations: true,
		Receive: &Receive{Type: "forwarding", Destination: []string{"a@x.com"}},
	})
	if err != nil || got.Username != "marta" || got.HasPassword {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestDeleteIdentity(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		if r.Method != http.MethodDelete || r.URL.Path != "/zones/example.com/identities/id-1" {
			t.Fatal(r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok"}`)
	})
	if err := c.DeleteIdentity(context.Background(), "example.com", "id-1"); err != nil {
		t.Fatal(err)
	}
}

func TestSendAsPermissions(t *testing.T) {
	var seen []string
	c := serve(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		io.WriteString(w, `{"status":"ok","identity":{"id":"id-1","username":"info","enabled":true,"password_hash":null,"mailbox":null,"forwarding":null,"authorizations":["sender@example.com"]}}`)
	})

	allowed, err := c.AllowSendAs(context.Background(), "example.com", "id-1", "cred-9")
	if err != nil || len(allowed.Authorizations) != 1 {
		t.Fatalf("%+v %v", allowed, err)
	}
	if _, err := c.RevokeSendAs(context.Background(), "example.com", "id-1", "cred-9"); err != nil {
		t.Fatal(err)
	}
	want := []string{"PUT /zones/example.com/identities/id-1/send_as/cred-9", "DELETE /zones/example.com/identities/id-1/send_as/cred-9"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", seen)
	}
}

func TestStorages(t *testing.T) {
	var seen []string
	c := serve(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			io.WriteString(w, `{"status":"ok","storages":[{"id":"s1","name":"old","abandoned_at":"2026-11-02T10:00:00Z","purge_after":"2026-12-02T10:00:00Z","days_left":27,"usage_bytes":734003200}]}`)
		case http.MethodPost:
			if body["username"] != "again" || body["password"] != "long-enough-1" {
				t.Fatalf("%v", body)
			}
			io.WriteString(w, `{"status":"ok","identity":{"id":"n","username":"again","enabled":true,"password_hash":"h","mailbox":{"quota_tier":"5gb","quota_used_mb":0,"storage_key":"k"},"forwarding":null,"authorizations":[]}}`)
		default:
			io.WriteString(w, `{"status":"ok"}`)
		}
	})
	ctx := context.Background()

	storages, err := c.ListStorages(ctx, "example.com")
	if err != nil || len(storages) != 1 || storages[0].DaysLeft != 27 || storages[0].UsageBytes == nil || *storages[0].UsageBytes != 734003200 {
		t.Fatalf("%+v %v", storages, err)
	}
	identity, err := c.AttachStorage(ctx, "example.com", "s1", AttachInput{Username: "again", Password: "long-enough-1"})
	if err != nil || identity.Mailbox == nil {
		t.Fatalf("%+v %v", identity, err)
	}
	if err := c.DeleteStorage(ctx, "example.com", "s1"); err != nil {
		t.Fatal(err)
	}

	want := "GET /zones/example.com/mail_storages|POST /zones/example.com/mail_storages/s1/attach|DELETE /zones/example.com/mail_storages/s1"
	if strings.Join(seen, "|") != want {
		t.Fatalf("%v", seen)
	}
}
