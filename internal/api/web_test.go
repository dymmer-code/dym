package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func webServer(t *testing.T, check func(r *http.Request, body map[string]any), reply string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("the body is not JSON: %s", raw)
			}
		}
		check(r, body)
		if status != 0 {
			w.WriteHeader(status)
		}
		io.WriteString(w, reply)
	}))
}

const webURIReply = `{"id":"u1","uri":"/","type":"proxy","target":"http://192.168.1.57:4016","tls":"required","state":"pending","error_reason":null,"applied_at":null,"errored_at":null,"updated_at":"2026-10-14T10:00:00.000001Z"}`

func TestListWebSitesAsksForTheNameOfTheZoneAndTheServer(t *testing.T) {
	s := webServer(t, func(r *http.Request, _ map[string]any) {
		if r.Method != http.MethodGet || r.URL.Path != "/zones/example.org/web_servers/maple/uris" || r.URL.Query().Get("host") != "api" {
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}, `{"status":"ok","sites":[{"host":"api.example.org","uris":[`+webURIReply+`]}]}`, 0)
	defer s.Close()

	sites, err := NewClient(s.URL, "tok", s.Client()).ListWebSites(context.Background(), "example.org", "maple", "api")
	if err != nil || len(sites) != 1 || sites[0].Host != "api.example.org" || sites[0].URIs[0].State != "pending" {
		t.Fatalf("%+v %v", sites, err)
	}
}

func TestTheServerCanBeLeftOutWithADash(t *testing.T) {
	s := webServer(t, func(r *http.Request, _ map[string]any) {
		if r.URL.Path != "/zones/example.org/web_servers/-/uris" {
			t.Fatal(r.URL.Path)
		}
	}, `{"status":"ok","sites":[]}`, 0)
	defer s.Close()
	if _, err := NewClient(s.URL, "tok", s.Client()).ListWebSites(context.Background(), "example.org", "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWebURISendsOnlyWhatWasSet(t *testing.T) {
	s := webServer(t, func(r *http.Request, body map[string]any) {
		web, _ := body["web"].(map[string]any)
		if r.Method != http.MethodPost || r.URL.Path != "/zones/example.org/web_servers/maple/uris" || web["host"] != "api" ||
			web["target"] != "http://192.168.1.57:4016" || len(web) != 2 {
			t.Fatalf("%s %s %v", r.Method, r.URL.Path, body)
		}
		if _, set := body["dry_run"]; set {
			t.Fatalf("dry_run was not asked for: %v", body)
		}
	}, `{"status":"ok","uri":`+webURIReply+`,"warnings":["api.example.org leads to 1.2.3.4"],"dry_run":false}`, 0)
	defer s.Close()

	result, err := NewClient(s.URL, "tok", s.Client()).CreateWebURI(context.Background(), "example.org", "maple",
		map[string]string{"host": "api", "target": "http://192.168.1.57:4016"}, false)
	if err != nil || result.URI.ID != "u1" || len(result.Warnings) != 1 || result.DryRun {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestADryRunIsAskedFor(t *testing.T) {
	s := webServer(t, func(r *http.Request, body map[string]any) {
		if body["dry_run"] != true {
			t.Fatalf("%v", body)
		}
	}, `{"status":"ok","uri":`+webURIReply+`,"warnings":[],"dry_run":true}`, 0)
	defer s.Close()

	result, err := NewClient(s.URL, "tok", s.Client()).CreateWebURI(context.Background(), "example.org", "-",
		map[string]string{"target": "http://192.168.1.57:4016"}, true)
	if err != nil || !result.DryRun {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestUpdateRemoveAndRestore(t *testing.T) {
	var seen []string
	s := webServer(t, func(r *http.Request, body map[string]any) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
	}, `{"status":"ok","uri":`+webURIReply+`,"warnings":[],"dry_run":false}`, 0)
	defer s.Close()
	c := NewClient(s.URL, "tok", s.Client())
	ctx := context.Background()

	if _, err := c.UpdateWebURI(ctx, "example.org", "maple", "u1", map[string]string{"tls": "enabled"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteWebURI(ctx, "example.org", "maple", "u1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RestoreWebURI(ctx, "example.org", "maple", "u1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PUT /zones/example.org/web_servers/maple/uris/u1",
		"DELETE /zones/example.org/web_servers/maple/uris/u1?dry_run=true",
		"POST /zones/example.org/web_servers/maple/uris/u1/restore",
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("%d: got %q want %q", i, seen[i], want[i])
		}
	}
}

func TestListWebServers(t *testing.T) {
	s := webServer(t, func(r *http.Request, _ map[string]any) {
		if r.URL.Path != "/web_servers" {
			t.Fatal(r.URL.Path)
		}
	}, `{"status":"ok","web_servers":[{"id":"w1","name":"maple","hostname":"maple.altenwald.com","maintenance":false}]}`, 0)
	defer s.Close()

	servers, err := NewClient(s.URL, "tok", s.Client()).ListWebServers(context.Background())
	if err != nil || len(servers) != 1 || servers[0].Name != "maple" || servers[0].Hostname != "maple.altenwald.com" {
		t.Fatalf("%+v %v", servers, err)
	}
}

func TestAValidationErrorKeepsTheMessagesByField(t *testing.T) {
	s := webServer(t, func(*http.Request, map[string]any) {},
		`{"status":"error","reason":"validation","errors":{"target":["10.9.9.9 is not the address of a host of yours"]}}`, http.StatusBadRequest)
	defer s.Close()

	_, err := NewClient(s.URL, "tok", s.Client()).CreateWebURI(context.Background(), "example.org", "-", map[string]string{"target": "x"}, false)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 || len(apiErr.Errors["target"]) != 1 {
		t.Fatalf("%v", err)
	}
}

func TestGetWebOptionsReadsTheOptionsOfTheName(t *testing.T) {
	s := webServer(t, func(r *http.Request, _ map[string]any) {
		if r.Method != http.MethodGet || r.URL.Path != "/zones/example.org/web_servers/-/options/@" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
	}, `{"status":"ok","host":"example.org","options":{"hsts":true,"asset_extensions":"jpg|css"},"dry_run":false}`, 0)
	defer s.Close()

	got, err := NewClient(s.URL, "tok", s.Client()).GetWebOptions(context.Background(), "example.org", "", "")
	if err != nil || got.Host != "example.org" || got.Options["hsts"] != true || got.Options["asset_extensions"] != "jpg|css" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSetWebOptionsSendsNilToTakeAnOptionAway(t *testing.T) {
	s := webServer(t, func(r *http.Request, body map[string]any) {
		if r.Method != http.MethodPut || r.URL.Path != "/zones/example.org/web_servers/maple/options/games" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		options := body["options"].(map[string]any)
		if v, present := options["blog_fallback"]; !present || v != nil || options["hsts"] != true || body["dry_run"] != true {
			t.Fatalf("body = %v", body)
		}
	}, `{"status":"ok","host":"games.example.org","options":{"hsts":true},"dry_run":true}`, 0)
	defer s.Close()

	got, err := NewClient(s.URL, "tok", s.Client()).SetWebOptions(context.Background(), "example.org", "maple", "games",
		map[string]any{"hsts": true, "blog_fallback": nil}, true)
	if err != nil || !got.DryRun {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestTheCodeOfARedirectGoesAsANumber(t *testing.T) {
	s := webServer(t, func(r *http.Request, body map[string]any) {
		web := body["web"].(map[string]any)
		if web["redirect_status"] != float64(308) || web["type"] != "redirect" {
			t.Fatalf("web = %v (%T)", web, web["redirect_status"])
		}
	}, `{"status":"ok","uri":{"id":"r1","uri":"/","type":"redirect","target":"https://x.example.org","tls":"required","redirect_status":308,"state":"pending"},"warnings":[],"dry_run":false}`, 0)
	defer s.Close()

	got, err := NewClient(s.URL, "tok", s.Client()).CreateWebURI(context.Background(), "example.org", "", map[string]string{
		"type": "redirect", "target": "https://x.example.org", "redirect_status": "308"}, false)
	if err != nil || got.URI.RedirectStatus != 308 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestListWebBucketsAsksTheBucketsOfTheUser(t *testing.T) {
	s := webServer(t, func(r *http.Request, _ map[string]any) {
		if r.Method != http.MethodGet || r.URL.Path != "/web_buckets" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
	}, `{"status":"ok","buckets":[{"name":"altenwald-org"}]}`, 0)
	defer s.Close()

	got, err := NewClient(s.URL, "tok", s.Client()).ListWebBuckets(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "altenwald-org" {
		t.Fatalf("%+v %v", got, err)
	}
}
