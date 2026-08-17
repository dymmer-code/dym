package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUpdateRecordSendsTypeAndTTL(t *testing.T) {
	ttl := 7200
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/zones/example.com/records/rec-1" {
			t.Fatal(r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		got := string(body)
		for _, want := range []string{`"type":"TXT"`, `"content_value":"hello"`, `"time_to_live":7200`} {
			if !strings.Contains(got, want) {
				t.Fatalf("body %s missing %s", got, want)
			}
		}
		io.WriteString(w, `{"status":"ok","record":{"id":"rec-1","type":"TXT","host":"@","time_to_live":7200,"content":{"value":"hello"}}}`)
	}))
	defer s.Close()

	rec, err := NewClient(s.URL, "tok", s.Client()).UpdateRecord(context.Background(), "example.com", "rec-1", RecordInput{
		Type:    "TXT",
		TTL:     &ttl,
		Content: map[string]string{"value": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.TimeToLive != 7200 {
		t.Fatalf("unexpected ttl: %+v", rec)
	}
}

func TestDeleteRecordReturnsDeletedRecord(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/zones/example.com/records/rec-9" {
			t.Fatal(r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok","record":{"id":"rec-9","type":"A","host":"www","time_to_live":3600,"content":{"ip":"1.2.3.4"}}}`)
	}))
	defer s.Close()

	rec, err := NewClient(s.URL, "tok", s.Client()).DeleteRecord(context.Background(), "example.com", "rec-9")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "rec-9" {
		t.Fatalf("unexpected record: %+v", rec)
	}
}

func TestListRecordsNormalizesAbsentRecordsToEmptySlice(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"ok"}`)
	}))
	defer s.Close()

	records, err := NewClient(s.URL, "tok", s.Client()).ListRecords(context.Background(), "example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if records == nil {
		t.Fatal("ListRecords must return an empty slice, not nil, for an absent records field")
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("encoded = %s, want []", encoded)
	}
}

func TestListMailboxesNormalizesAbsentMailboxesToEmptySlice(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"ok"}`)
	}))
	defer s.Close()

	boxes, err := NewClient(s.URL, "tok", s.Client()).ListMailboxes(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if boxes == nil {
		t.Fatal("ListMailboxes must return an empty slice, not nil, for an absent mailboxes field")
	}
	encoded, err := json.Marshal(boxes)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("encoded = %s, want []", encoded)
	}
}

func TestListForwardingsNormalizesAbsentForwardingsToEmptySlice(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"ok"}`)
	}))
	defer s.Close()

	fwds, err := NewClient(s.URL, "tok", s.Client()).ListForwardings(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if fwds == nil {
		t.Fatal("ListForwardings must return an empty slice, not nil, for an absent forwardings field")
	}
	encoded, err := json.Marshal(fwds)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("encoded = %s, want []", encoded)
	}
}

func TestListMailboxes(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/example.com/mailboxes" {
			t.Fatal(r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok","mailboxes":[{"username":"alice","password_md5":"5f4dcc3b5aa765d61d8327deb882cf99","enabled":true}]}`)
	}))
	defer s.Close()

	boxes, err := NewClient(s.URL, "tok", s.Client()).ListMailboxes(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 || boxes[0].Username != "alice" || boxes[0].PasswordMD5 != "5f4dcc3b5aa765d61d8327deb882cf99" || !boxes[0].Enabled {
		t.Fatalf("unexpected mailboxes: %+v", boxes)
	}
}

func TestListForwardingsDestinationIsSlice(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/example.com/forwardings" {
			t.Fatal(r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok","forwardings":[{"username":"admin","destination":["first@example.com","second@example.com"],"enabled":true}]}`)
	}))
	defer s.Close()

	fwds, err := NewClient(s.URL, "tok", s.Client()).ListForwardings(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(fwds) != 1 || len(fwds[0].Destination) != 2 || fwds[0].Destination[0] != "first@example.com" {
		t.Fatalf("unexpected forwardings: %+v", fwds)
	}
}

func TestGetSecretsJSONMode(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hosts/secrets" {
			t.Fatal(r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("project") != "myproj" || q.Get("env") != "dev" {
			t.Fatalf("unexpected query: %v", q)
		}
		if _, ok := q["deployment"]; ok {
			t.Fatalf("deployment must be omitted entirely when unset, got %v", q)
		}
		io.WriteString(w, `[{"key":"FOO","value":"bar","comments":"","removed":false}]`)
	}))
	defer s.Close()

	result, err := NewClient(s.URL, "tok", s.Client()).GetSecrets(context.Background(), "myproj", "dev", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Raw != "" {
		t.Fatalf("expected no raw content in JSON mode, got %q", result.Raw)
	}
	if len(result.Entries) != 1 || result.Entries[0].Key != "FOO" || result.Entries[0].Value != "bar" {
		t.Fatalf("unexpected entries: %+v", result.Entries)
	}
}

func TestGetSecretsDotenvModeSendsDotEnvFormat(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("format") != ".env" {
			t.Fatalf("expected format=.env, got %q", q.Get("format"))
		}
		if q.Get("deployment") != "prod-1" {
			t.Fatalf("expected deployment=prod-1, got %q", q.Get("deployment"))
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "FOO=bar\nBAZ=qux\n")
	}))
	defer s.Close()

	result, err := NewClient(s.URL, "tok", s.Client()).GetSecrets(context.Background(), "myproj", "dev", "prod-1", ".env")
	if err != nil {
		t.Fatal(err)
	}
	if result.Raw != "FOO=bar\nBAZ=qux\n" {
		t.Fatalf("unexpected raw body: %q", result.Raw)
	}
	if result.Entries != nil {
		t.Fatalf("expected no entries in dotenv mode, got %+v", result.Entries)
	}
}

func TestGetSecretsEmptyResultsStillOK(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[]`)
	}))
	defer s.Close()

	result, err := NewClient(s.URL, "tok", s.Client()).GetSecrets(context.Background(), "myproj", "dev", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("expected empty entries, got %+v", result.Entries)
	}
}

func TestGetSecretsUsesFlatErrorEnvelope(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"Project not found"}`)
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "tok", s.Client()).GetSecrets(context.Background(), "nope", "dev", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Reason, "Project not found") {
		t.Fatalf("expected reason to mention Project not found, got %q", apiErr.Reason)
	}
}

func TestGetSecretsConflictAmbiguousDeployment(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"Two or more deployments with the same name"}`)
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "tok", s.Client()).GetSecrets(context.Background(), "myproj", "dev", "ambiguous", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", apiErr.StatusCode)
	}
}

func TestGetSecretsDotenvErrorIsPlainText(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "Deployment not found")
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "tok", s.Client()).GetSecrets(context.Background(), "myproj", "dev", "missing", ".env")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", apiErr.StatusCode)
	}
}

func TestValidationErrorExposesFieldErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"status":"error","reason":"validation","errors":{"content_ip":["is invalid"]}}`)
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "tok", s.Client()).CreateRecord(context.Background(), "example.com", RecordInput{
		Type:    "A",
		Content: map[string]string{"ip": "not-an-ip"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", apiErr.StatusCode)
	}
	if len(apiErr.Errors["content_ip"]) != 1 || apiErr.Errors["content_ip"][0] != "is invalid" {
		t.Fatalf("expected field errors, got %+v", apiErr.Errors)
	}
}

func TestNotFoundErrorHasEmptyErrorsMap(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"status":"error","reason":"not found","errors":{}}`)
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "tok", s.Client()).ListRecords(context.Background(), "example.com", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", apiErr.StatusCode)
	}
}

func TestErrorMessageNeverIncludesAuthHeaderOrToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"status":"error","reason":"conflict","errors":{}}`)
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "top-secret-value", s.Client()).CreateRecord(context.Background(), "example.com", RecordInput{Type: "A"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "top-secret-value") || strings.Contains(msg, "Authorization") || strings.Contains(msg, "Bearer") {
		t.Fatalf("error leaked sensitive data: %q", msg)
	}
}

func TestMalformedNonJSONBodyDoesNotPanic(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "<html>server exploded</html>")
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "tok", s.Client()).ListRecords(context.Background(), "example.com", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", apiErr.StatusCode)
	}
}

func TestListRecordsEscapesDomainSegment(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the raw path segment is escaped and decodes back correctly.
		decoded, err := url.PathUnescape(strings.TrimPrefix(strings.SplitN(r.URL.EscapedPath(), "/records", 2)[0], "/zones/"))
		if err != nil {
			t.Fatal(err)
		}
		if decoded != "exa mple.com" {
			t.Fatalf("unexpected decoded domain: %q (path=%s)", decoded, r.URL.EscapedPath())
		}
		io.WriteString(w, `{"status":"ok","records":[]}`)
	}))
	defer s.Close()

	if _, err := NewClient(s.URL, "tok", s.Client()).ListRecords(context.Background(), "exa mple.com", ""); err != nil {
		t.Fatal(err)
	}
}
