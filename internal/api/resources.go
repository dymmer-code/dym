package api

import (
	"context"
	"net/http"
	"net/url"
)

// Record is a single DNS record as returned by the Dymmer API. Content
// holds the record's type-specific fields (e.g. "ip" for an A record,
// "value" for a TXT record) as returned by the server, keyed without the
// "content_" prefix used on the wire for writes.
type Record struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Host       string            `json:"host"`
	TimeToLive int               `json:"time_to_live"`
	Content    map[string]string `json:"content"`
}

// RecordInput describes the fields a caller wants to set when creating or
// updating a record. Content keys are the bare field names (e.g. "ip",
// "value", "priority") — CreateRecord/UpdateRecord serialize each one as a
// top-level "content_<key>" field on the request body, so this package
// never needs to know which record types accept which fields; that
// mapping lives in the CLI command layer.
//
// Host and TTL are optional: an empty Host is omitted from the request
// (the server defaults it to "@"), and a nil TTL is omitted (the server
// defaults it to 3600). Type is always sent, even on updates where it is
// unchanged, since the server uses it to select content validation.
type RecordInput struct {
	Type    string
	Host    string
	TTL     *int
	Content map[string]string
}

type recordEnvelope struct {
	Status string `json:"status"`
	Record Record `json:"record"`
}

type recordsEnvelope struct {
	Status  string   `json:"status"`
	Records []Record `json:"records"`
}

func recordsPath(domain string) string {
	return "/zones/" + url.PathEscape(domain) + "/records"
}

func recordPath(domain, id string) string {
	return recordsPath(domain) + "/" + url.PathEscape(id)
}

func buildRecordBody(input RecordInput) map[string]any {
	record := map[string]any{"type": input.Type}
	if input.Host != "" {
		record["host"] = input.Host
	}
	if input.TTL != nil {
		record["time_to_live"] = *input.TTL
	}
	for key, value := range input.Content {
		record["content_"+key] = value
	}
	return map[string]any{"record": record}
}

// ListRecords fetches the records for domain, optionally filtered by exact
// record type match (e.g. "A"). An empty recordType omits the filter.
func (c *Client) ListRecords(ctx context.Context, domain, recordType string) ([]Record, error) {
	path := recordsPath(domain)
	if recordType != "" {
		path += "?" + url.Values{"type": {recordType}}.Encode()
	}
	var resp recordsEnvelope
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Records == nil {
		resp.Records = []Record{}
	}
	return resp.Records, nil
}

// CreateRecord creates a new record on domain.
func (c *Client) CreateRecord(ctx context.Context, domain string, input RecordInput) (*Record, error) {
	var resp recordEnvelope
	if err := c.do(ctx, http.MethodPost, recordsPath(domain), buildRecordBody(input), &resp); err != nil {
		return nil, err
	}
	return &resp.Record, nil
}

// UpdateRecord replaces the record identified by id on domain.
func (c *Client) UpdateRecord(ctx context.Context, domain, id string, input RecordInput) (*Record, error) {
	var resp recordEnvelope
	if err := c.do(ctx, http.MethodPut, recordPath(domain, id), buildRecordBody(input), &resp); err != nil {
		return nil, err
	}
	return &resp.Record, nil
}

// DeleteRecord removes the record identified by id on domain and returns
// the record as it existed just before deletion.
func (c *Client) DeleteRecord(ctx context.Context, domain, id string) (*Record, error) {
	var resp recordEnvelope
	if err := c.do(ctx, http.MethodDelete, recordPath(domain, id), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Record, nil
}

// Mailbox is a single mailbox as returned by the Dymmer API. PasswordMD5 is
// an opaque hash, not a plaintext credential.
type Mailbox struct {
	Username    string `json:"username"`
	PasswordMD5 string `json:"password_md5"`
	Enabled     bool   `json:"enabled"`
}

// ListMailboxes fetches the mailboxes configured for domain.
func (c *Client) ListMailboxes(ctx context.Context, domain string) ([]Mailbox, error) {
	var resp struct {
		Status    string    `json:"status"`
		Mailboxes []Mailbox `json:"mailboxes"`
	}
	path := "/zones/" + url.PathEscape(domain) + "/mailboxes"
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Mailboxes == nil {
		resp.Mailboxes = []Mailbox{}
	}
	return resp.Mailboxes, nil
}

// Forwarding is a single mail forwarding rule as returned by the Dymmer
// API. Destination can fan out to multiple addresses.
type Forwarding struct {
	Username    string   `json:"username"`
	Destination []string `json:"destination"`
	Enabled     bool     `json:"enabled"`
}

// ListForwardings fetches the mail forwardings configured for domain.
func (c *Client) ListForwardings(ctx context.Context, domain string) ([]Forwarding, error) {
	var resp struct {
		Status      string       `json:"status"`
		Forwardings []Forwarding `json:"forwardings"`
	}
	path := "/zones/" + url.PathEscape(domain) + "/forwardings"
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Forwardings == nil {
		resp.Forwardings = []Forwarding{}
	}
	return resp.Forwardings, nil
}

// SecretEntry is a single secret as returned by the Dymmer API's JSON
// secrets format.
type SecretEntry struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Comments string `json:"comments"`
	Removed  bool   `json:"removed"`
}

// SecretsResult holds the outcome of GetSecrets. Exactly one of Raw or
// Entries is populated, depending on which format was requested: Raw holds
// the verbatim dotenv text when format was ".env"; Entries holds the
// decoded array for JSON mode.
type SecretsResult struct {
	Raw     string
	Entries []SecretEntry
}

// GetSecrets fetches secrets for project (required), env, and optionally
// deployment (omitted from the request entirely when empty).
//
// format must be the *wire* value the Dymmer server understands, not a
// CLI-facing name: pass "" or "json" for JSON output, and the literal
// string ".env" for dotenv output. The server only recognizes the exact
// string ".env" to mean dotenv; any other value (including the strings
// "json" or "dotenv") falls back to JSON. Callers translating a
// user-facing --format flag (e.g. "dotenv") must map it to ".env"
// themselves before calling GetSecrets — this package does not do that
// translation, to keep the CLI/server format-name mismatch visible at the
// call site instead of hidden here.
func (c *Client) GetSecrets(ctx context.Context, project, env, deployment, format string) (*SecretsResult, error) {
	query := url.Values{}
	query.Set("project", project)
	query.Set("env", env)
	if deployment != "" {
		query.Set("deployment", deployment)
	}
	if format != "" {
		query.Set("format", format)
	}
	path := "/hosts/secrets?" + query.Encode()

	if format == ".env" {
		raw, err := c.doRaw(ctx, http.MethodGet, path)
		if err != nil {
			return nil, err
		}
		return &SecretsResult{Raw: raw}, nil
	}

	var entries []SecretEntry
	if err := c.do(ctx, http.MethodGet, path, nil, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []SecretEntry{}
	}
	return &SecretsResult{Entries: entries}, nil
}
