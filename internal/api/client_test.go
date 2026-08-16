package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListRecordsSendsBearerAndFilter(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.URL.String() != "/zones/example.com/records?type=A" {
			t.Fatal(r.URL)
		}
		io.WriteString(w, `{"status":"ok","records":[]}`)
	}))
	defer s.Close()

	records, err := NewClient(s.URL, "test-token", s.Client()).ListRecords(context.Background(), "example.com", "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(records))
	}
}

func TestListRecordsWithoutFilterOmitsQuery(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "/zones/example.com/records" {
			t.Fatal(r.URL)
		}
		io.WriteString(w, `{"status":"ok","records":[]}`)
	}))
	defer s.Close()

	if _, err := NewClient(s.URL, "tok", s.Client()).ListRecords(context.Background(), "example.com", ""); err != nil {
		t.Fatal(err)
	}
}

func TestDoesNotHardcodeAPIPrefix(t *testing.T) {
	// baseURL already includes an API prefix; NewClient must not add its own.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/zones/example.com/records" {
			t.Fatal(r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok","records":[]}`)
	}))
	defer s.Close()

	if _, err := NewClient(s.URL+"/api/v1", "tok", s.Client()).ListRecords(context.Background(), "example.com", ""); err != nil {
		t.Fatal(err)
	}
}

func Test401PlainTextBodyDoesNotPanicAndHidesToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "No access for you")
	}))
	defer s.Close()

	_, err := NewClient(s.URL, "super-secret-token", s.Client()).ListRecords(context.Background(), "example.com", "")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "super-secret-token") || strings.Contains(msg, "Authorization") || strings.Contains(msg, "Bearer") {
		t.Fatalf("error leaked sensitive data: %q", msg)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", apiErr.StatusCode)
	}
}

func TestNotFoundWithoutBodyPreservesAmbiguity(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
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
	if apiErr.Reason != "not found or not accessible" {
		t.Fatalf("reason = %q, want %q", apiErr.Reason, "not found or not accessible")
	}
}

func TestNetworkFailureIncludesRetrySuggestion(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	s.Close() // closed immediately: any request against it fails at the transport level.

	_, err := NewClient(s.URL, "tok", s.Client()).ListRecords(context.Background(), "example.com", "")
	if err == nil {
		t.Fatal("expected a transport-level error")
	}
	if !strings.Contains(err.Error(), "check your network connection and try again") {
		t.Fatalf("error = %q, want a retry suggestion", err.Error())
	}
}

func TestAPIErrorFieldsAreSortedForDeterministicOutput(t *testing.T) {
	apiErr := &APIError{
		StatusCode: 400,
		Reason:     "validation",
		Errors: map[string][]string{
			"zeta":  {"z err"},
			"alpha": {"a err"},
			"mid":   {"m err"},
		},
	}
	want := `dymmer api: validation (status 400) [alpha: [a err], mid: [m err], zeta: [z err]]`
	if got := apiErr.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestCreateRecordEncodesFlatContentFields(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		got := string(body)
		for _, want := range []string{`"type":"A"`, `"content_ip":"127.0.0.1"`, `"host":"www"`} {
			if !strings.Contains(got, want) {
				t.Fatalf("body %s missing %s", got, want)
			}
		}
		if strings.Contains(got, `"domain_id"`) {
			t.Fatalf("body must never send domain_id: %s", got)
		}
		io.WriteString(w, `{"status":"ok","record":{"id":"abc","type":"A","host":"www","time_to_live":3600,"content":{"ip":"127.0.0.1"}}}`)
	}))
	defer s.Close()

	rec, err := NewClient(s.URL, "tok", s.Client()).CreateRecord(context.Background(), "example.com", RecordInput{
		Type: "A",
		Host: "www",
		Content: map[string]string{
			"ip": "127.0.0.1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "abc" || rec.Content["ip"] != "127.0.0.1" {
		t.Fatalf("unexpected record: %+v", rec)
	}
}
