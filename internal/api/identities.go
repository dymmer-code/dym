package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// IdentityMailbox is the mailbox of an identity.
type IdentityMailbox struct {
	QuotaTier   string `json:"quota_tier"`
	QuotaUsedMB int    `json:"quota_used_mb"`
	StorageKey  string `json:"storage_key"`
}

// IdentityForwarding is the forwarding of an identity.
type IdentityForwarding struct {
	Destination []string `json:"destination"`
}

// Identity is a mail address of a zone with what it can do: send with a password, receive in a mailbox or forward,
// and the addresses that send as it. The API also returns the hash of the password, for mail servers; it is never
// kept here: only whether there is one.
type Identity struct {
	ID             string              `json:"id"`
	Username       string              `json:"username"`
	Enabled        bool                `json:"enabled"`
	HasPassword    bool                `json:"has_password"`
	Mailbox        *IdentityMailbox    `json:"mailbox"`
	Forwarding     *IdentityForwarding `json:"forwarding"`
	Authorizations []string            `json:"authorizations"`
}

// UnmarshalJSON reads an identity as the API sends it, dropping the password hash.
func (i *Identity) UnmarshalJSON(data []byte) error {
	type plain Identity
	aux := struct {
		*plain
		PasswordHash *string `json:"password_hash"`
	}{plain: (*plain)(i)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.PasswordHash != nil {
		i.HasPassword = true
	}
	return nil
}

// Receive says how an identity receives mail: Type is "mailbox", "forwarding" or "none". StorageID (a deleted
// mailbox to take back) only goes with "mailbox", and Destination with "forwarding".
type Receive struct {
	Type        string
	StorageID   string
	Destination []string
}

// IdentityInput is what a caller wants to set when creating or changing an identity. Nil and empty fields are not
// sent, so that a change only touches what it names. RemovePassword stops the identity from sending by itself.
type IdentityInput struct {
	Username           *string
	Enabled            *bool
	Password           *string
	RemovePassword     bool
	Receive            *Receive
	UpdateDestinations bool
}

func buildIdentityBody(input IdentityInput) map[string]any {
	identity := map[string]any{}
	if input.Username != nil {
		identity["username"] = *input.Username
	}
	if input.Enabled != nil {
		identity["enabled"] = *input.Enabled
	}
	if input.RemovePassword {
		identity["password"] = nil
	} else if input.Password != nil {
		identity["password"] = *input.Password
	}
	if input.Receive != nil {
		receive := map[string]any{"type": input.Receive.Type}
		if input.Receive.StorageID != "" {
			receive["storage_id"] = input.Receive.StorageID
		}
		if input.Receive.Type == "forwarding" {
			receive["destination"] = input.Receive.Destination
		}
		identity["receive"] = receive
	}
	if input.UpdateDestinations {
		identity["update_destinations"] = true
	}
	return map[string]any{"identity": identity}
}

func identitiesPath(zone string) string { return "/zones/" + url.PathEscape(zone) + "/identities" }

func identityPath(zone, id string) string { return identitiesPath(zone) + "/" + url.PathEscape(id) }

type identityEnvelope struct {
	Status   string   `json:"status"`
	Identity Identity `json:"identity"`
}

// ListIdentities fetches the identities of zone.
func (c *Client) ListIdentities(ctx context.Context, zone string) ([]Identity, error) {
	var resp struct {
		Status     string     `json:"status"`
		Identities []Identity `json:"identities"`
	}
	if err := c.do(ctx, http.MethodGet, identitiesPath(zone), nil, &resp); err != nil {
		return nil, err
	}
	if resp.Identities == nil {
		resp.Identities = []Identity{}
	}
	return resp.Identities, nil
}

// CreateIdentity creates an identity on zone.
func (c *Client) CreateIdentity(ctx context.Context, zone string, input IdentityInput) (*Identity, error) {
	var resp identityEnvelope
	if err := c.do(ctx, http.MethodPost, identitiesPath(zone), buildIdentityBody(input), &resp); err != nil {
		return nil, err
	}
	return &resp.Identity, nil
}

// UpdateIdentity changes what input names of the identity id.
func (c *Client) UpdateIdentity(ctx context.Context, zone, id string, input IdentityInput) (*Identity, error) {
	var resp identityEnvelope
	if err := c.do(ctx, http.MethodPatch, identityPath(zone, id), buildIdentityBody(input), &resp); err != nil {
		return nil, err
	}
	return &resp.Identity, nil
}

// DeleteIdentity deletes the identity id for good; the storage of its mailbox, if it has one, is kept for a while.
func (c *Client) DeleteIdentity(ctx context.Context, zone, id string) error {
	return c.do(ctx, http.MethodDelete, identityPath(zone, id), nil, nil)
}

func sendAsPath(zone, id, credentialID string) string {
	return identityPath(zone, id) + "/send_as/" + url.PathEscape(credentialID)
}

// AllowSendAs lets the identity credentialID send as the identity id, and returns the latter.
func (c *Client) AllowSendAs(ctx context.Context, zone, id, credentialID string) (*Identity, error) {
	var resp identityEnvelope
	if err := c.do(ctx, http.MethodPut, sendAsPath(zone, id, credentialID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Identity, nil
}

// RevokeSendAs stops the identity credentialID from sending as the identity id, and returns the latter.
func (c *Client) RevokeSendAs(ctx context.Context, zone, id, credentialID string) (*Identity, error) {
	var resp identityEnvelope
	if err := c.do(ctx, http.MethodDelete, sendAsPath(zone, id, credentialID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Identity, nil
}

// Storage is a mailbox that nobody owns now, kept for a while so that it can be taken back.
type Storage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AbandonedAt string `json:"abandoned_at"`
	PurgeAfter  string `json:"purge_after"`
	DaysLeft    int    `json:"days_left"`
	UsageBytes  *int64 `json:"usage_bytes"`
}

func storagesPath(zone string) string { return "/zones/" + url.PathEscape(zone) + "/mail_storages" }

// ListStorages fetches the deleted mailboxes of zone that can still be taken back.
func (c *Client) ListStorages(ctx context.Context, zone string) ([]Storage, error) {
	var resp struct {
		Status   string    `json:"status"`
		Storages []Storage `json:"storages"`
	}
	if err := c.do(ctx, http.MethodGet, storagesPath(zone), nil, &resp); err != nil {
		return nil, err
	}
	if resp.Storages == nil {
		resp.Storages = []Storage{}
	}
	return resp.Storages, nil
}

// AttachInput says who takes a deleted mailbox back: a new identity (Username, with Password) or, for
// administrators, an existing one (IdentityID).
type AttachInput struct {
	Username   string
	IdentityID string
	Password   string
	Enabled    *bool
}

// AttachStorage gives the deleted mailbox id to an identity, with its mail, and returns the identity.
func (c *Client) AttachStorage(ctx context.Context, zone, id string, input AttachInput) (*Identity, error) {
	body := map[string]any{}
	if input.Username != "" {
		body["username"] = input.Username
	}
	if input.IdentityID != "" {
		body["identity_id"] = input.IdentityID
	}
	if input.Password != "" {
		body["password"] = input.Password
	}
	if input.Enabled != nil {
		body["enabled"] = *input.Enabled
	}
	var resp identityEnvelope
	if err := c.do(ctx, http.MethodPost, storagesPath(zone)+"/"+url.PathEscape(id)+"/attach", body, &resp); err != nil {
		return nil, err
	}
	return &resp.Identity, nil
}

// DeleteStorage deletes the mail of a deleted mailbox now, for good, and frees its place.
func (c *Client) DeleteStorage(ctx context.Context, zone, id string) error {
	return c.do(ctx, http.MethodDelete, storagesPath(zone)+"/"+url.PathEscape(id), nil, nil)
}
