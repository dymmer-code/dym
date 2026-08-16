// Package api implements a minimal HTTP client for the Dymmer hosting API.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client is a thin HTTP client for the Dymmer API. baseURL must already
// include any API path prefix (e.g. "https://dymmer.com/api/v1"); Client
// never adds one of its own, it only appends resource paths.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient builds a Client that authenticates every request with
// "Authorization: Bearer <token>".
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, token: token, httpClient: httpClient}
}

// APIError represents a non-2xx response from the Dymmer API. Its Error()
// message never includes request headers or the auth token: it is built
// exclusively from the response status code and (when parseable) the
// response body's reason/error fields.
type APIError struct {
	StatusCode int
	Reason     string
	Errors     map[string][]string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("dymmer api: %s (status %d)", e.Reason, e.StatusCode)
	if len(e.Errors) == 0 {
		return msg
	}
	first := true
	msg += " ["
	for field, fieldErrs := range e.Errors {
		if !first {
			msg += ", "
		}
		first = false
		msg += fmt.Sprintf("%s: %v", field, fieldErrs)
	}
	msg += "]"
	return msg
}

// do sends a JSON request (when body is non-nil) and decodes a JSON
// response into out (when out is non-nil). It never logs or includes
// request headers in returned errors.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	respBody, statusCode, err := c.send(req)
	if err != nil {
		return err
	}
	if statusCode < 200 || statusCode >= 300 {
		return decodeError(statusCode, respBody)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

// doRaw sends a request and returns the raw response body as a string,
// without attempting any JSON decoding. Used for the secrets endpoint's
// dotenv output mode.
func (c *Client) doRaw(ctx context.Context, method, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	respBody, statusCode, err := c.send(req)
	if err != nil {
		return "", err
	}
	if statusCode < 200 || statusCode >= 300 {
		return "", decodeError(statusCode, respBody)
	}
	return string(respBody), nil
}

// send executes req and returns the response body bytes and status code.
// Errors returned here are transport-level only (never include headers).
func (c *Client) send(req *http.Request) ([]byte, int, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("dymmer api request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read response body: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

// decodeError builds an *APIError from a non-2xx response body. It handles
// three body shapes without panicking or leaking anything but the parsed
// reason/status:
//
//  1. The zones/records/mailboxes/forwardings envelope:
//     {"status":"error","reason":"...","errors":{...}}
//  2. The secrets endpoint's flatter envelope: {"error":"..."}
//  3. Anything else (plain text, HTML, empty body — e.g. the 401
//     "No access for you" plain-text response): falls back to a generic
//     message derived only from the HTTP status code.
func decodeError(statusCode int, body []byte) error {
	var zonesEnvelope struct {
		Reason string              `json:"reason"`
		Errors map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(body, &zonesEnvelope); err == nil && zonesEnvelope.Reason != "" {
		return &APIError{StatusCode: statusCode, Reason: zonesEnvelope.Reason, Errors: zonesEnvelope.Errors}
	}

	var flatEnvelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &flatEnvelope); err == nil && flatEnvelope.Error != "" {
		return &APIError{StatusCode: statusCode, Reason: flatEnvelope.Error}
	}

	return &APIError{StatusCode: statusCode, Reason: genericReason(statusCode)}
}

func genericReason(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return "authentication failed"
	case http.StatusForbidden:
		return "access denied"
	case http.StatusNotFound:
		return "not found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusBadRequest:
		return "invalid request"
	default:
		return fmt.Sprintf("request failed with status %d", statusCode)
	}
}
