package api

import (
	"context"
	"net/http"
	"net/url"
)

// WebURI is one URI of a name and where it stands for its web server: State is "pending" (the server has not applied
// this version yet), "applied" or "errored", and ErrorReason says why the server refused it.
type WebURI struct {
	ID          string `json:"id"`
	URI         string `json:"uri"`
	Type        string `json:"type"`
	Target      string `json:"target"`
	TLS         string `json:"tls"`
	State       string `json:"state"`
	ErrorReason string `json:"error_reason"`
	AppliedAt   string `json:"applied_at"`
	ErroredAt   string `json:"errored_at"`
	UpdatedAt   string `json:"updated_at"`
}

// WebSite is a name with its URIs.
type WebSite struct {
	Host string   `json:"host"`
	URIs []WebURI `json:"uris"`
}

// WebServer is a web server the user can use.
type WebServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Hostname    string `json:"hostname"`
	Maintenance bool   `json:"maintenance"`
}

// WebResult is the answer to adding, changing, removing or restoring a URI: the URI, the warnings (a name whose DNS
// does not lead to the web server yet) and whether it was only a check (DryRun).
type WebResult struct {
	Status   string   `json:"status"`
	URI      WebURI   `json:"uri"`
	Warnings []string `json:"warnings"`
	DryRun   bool     `json:"dry_run"`
}

// AnyServer, as the web server of a call, lets the API choose the only one the zone can use.
const AnyServer = "-"

func webURIsPath(zone, server string) string {
	if server == "" {
		server = AnyServer
	}
	return "/zones/" + url.PathEscape(zone) + "/web_servers/" + url.PathEscape(server) + "/uris"
}

// ListWebSites lists the names of zone served by server with their URIs; host limits it to one name.
func (c *Client) ListWebSites(ctx context.Context, zone, server, host string) ([]WebSite, error) {
	path := webURIsPath(zone, server)
	if host != "" {
		path += "?" + url.Values{"host": {host}}.Encode()
	}
	var resp struct {
		Sites []WebSite `json:"sites"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Sites, nil
}

func webBody(fields map[string]string, dryRun bool) map[string]any {
	body := map[string]any{"web": fields}
	if dryRun {
		body["dry_run"] = true
	}
	return body
}

// CreateWebURI adds a URI. fields holds only what was set: host, uri, type, target, tls.
func (c *Client) CreateWebURI(ctx context.Context, zone, server string, fields map[string]string, dryRun bool) (*WebResult, error) {
	var resp WebResult
	if err := c.do(ctx, http.MethodPost, webURIsPath(zone, server), webBody(fields, dryRun), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateWebURI changes what a URI serves: only the fields given (uri, type, target, tls).
func (c *Client) UpdateWebURI(ctx context.Context, zone, server, id string, fields map[string]string, dryRun bool) (*WebResult, error) {
	var resp WebResult
	if err := c.do(ctx, http.MethodPut, webURIsPath(zone, server)+"/"+url.PathEscape(id), webBody(fields, dryRun), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteWebURI removes a URI (the server keeps it for a while, so it can be restored).
func (c *Client) DeleteWebURI(ctx context.Context, zone, server, id string, dryRun bool) (*WebResult, error) {
	path := webURIsPath(zone, server) + "/" + url.PathEscape(id)
	if dryRun {
		path += "?dry_run=true"
	}
	var resp WebResult
	if err := c.do(ctx, http.MethodDelete, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RestoreWebURI brings back a removed URI, if nobody took its place meanwhile.
func (c *Client) RestoreWebURI(ctx context.Context, zone, server, id string) (*WebResult, error) {
	var resp WebResult
	if err := c.do(ctx, http.MethodPost, webURIsPath(zone, server)+"/"+url.PathEscape(id)+"/restore", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListWebServers lists the web servers the user can use.
func (c *Client) ListWebServers(ctx context.Context) ([]WebServer, error) {
	var resp struct {
		WebServers []WebServer `json:"web_servers"`
	}
	if err := c.do(ctx, http.MethodGet, "/web_servers", nil, &resp); err != nil {
		return nil, err
	}
	return resp.WebServers, nil
}
