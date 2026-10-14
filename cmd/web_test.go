package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dymmer-code/dym/internal/api"
)

func runWeb(t *testing.T, fake *fakeAPI, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: errOut, API: fake})
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func site(host string, uris ...api.WebURI) api.WebSite { return api.WebSite{Host: host, URIs: uris} }

func uri(id, state, reason string) api.WebURI {
	return api.WebURI{ID: id, URI: "/", Type: "proxy", Target: "http://192.168.1.57:4016", TLS: "required", State: state, ErrorReason: reason}
}

func TestWebAddSendsOnlyWhatWasGiven(t *testing.T) {
	fake := &fakeAPI{}
	out, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "add", "fourcards", "--target", "http://192.168.1.57:4016")
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastWebZone != "altenwald.com" || fake.lastWebServer != "" || fake.lastWebDryRun {
		t.Fatalf("%q %q %v", fake.lastWebZone, fake.lastWebServer, fake.lastWebDryRun)
	}
	want := map[string]string{"host": "fourcards", "target": "http://192.168.1.57:4016"}
	if len(fake.lastWebFields) != 2 || fake.lastWebFields["host"] != want["host"] || fake.lastWebFields["target"] != want["target"] {
		t.Fatalf("fields = %v: the type, the URI and the TLS are the server's defaults", fake.lastWebFields)
	}
	if !strings.Contains(out, "u1") || !strings.Contains(out, "pending") {
		t.Fatalf("the answer should show the URI and its state: %q", out)
	}
}

func TestWebAddTakesEveryOption(t *testing.T) {
	fake := &fakeAPI{}
	_, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "api", "--server", "maple", "--uri", "/v1", "--type", "static",
		"--target", "garage://bucket/site/", "--tls", "enabled")
	if err != nil {
		t.Fatal(err)
	}
	f := fake.lastWebFields
	if fake.lastWebServer != "maple" || f["uri"] != "/v1" || f["type"] != "static" || f["tls"] != "enabled" || f["host"] != "api" {
		t.Fatalf("%q %v", fake.lastWebServer, f)
	}
}

func TestWebAddWithoutAHostIsTheApex(t *testing.T) {
	fake := &fakeAPI{}
	if _, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "--target", "http://192.168.1.57:80"); err != nil {
		t.Fatal(err)
	}
	if _, set := fake.lastWebFields["host"]; set {
		t.Fatalf("no host given, the server takes @: %v", fake.lastWebFields)
	}
}

func TestWebNoSslIsDisabledAndCannotMeetTls(t *testing.T) {
	fake := &fakeAPI{}
	if _, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "--target", "http://192.168.1.57:80", "--no-ssl"); err != nil {
		t.Fatal(err)
	}
	if fake.lastWebFields["tls"] != "disabled" {
		t.Fatalf("%v", fake.lastWebFields)
	}
	_, _, err := runWeb(t, &fakeAPI{}, "domain", "example.org", "web", "add", "--target", "http://x:80", "--no-ssl", "--tls", "required")
	if err == nil || !strings.Contains(err.Error(), "--no-ssl") {
		t.Fatalf("%v", err)
	}
}

func TestWebAddNeedsATargetAndAKnownTls(t *testing.T) {
	if _, _, err := runWeb(t, &fakeAPI{}, "domain", "example.org", "web", "add", "api"); err == nil || !strings.Contains(err.Error(), "--target") {
		t.Fatalf("%v", err)
	}
	if _, _, err := runWeb(t, &fakeAPI{}, "domain", "example.org", "web", "add", "--target", "http://x", "--tls", "maybe"); err == nil ||
		!strings.Contains(err.Error(), "--tls") {
		t.Fatalf("%v", err)
	}
}

func TestWebAddShowsTheWarningsOnStderr(t *testing.T) {
	fake := &fakeAPI{webResult: &api.WebResult{Status: "ok", URI: uri("u1", "pending", ""), Warnings: []string{"api.example.org leads to 1.2.3.4"}}}
	out, errOut, err := runWeb(t, fake, "domain", "example.org", "web", "add", "api", "--target", "http://x:80")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "warning: api.example.org leads to 1.2.3.4") || strings.Contains(out, "leads to") {
		t.Fatalf("out=%q err=%q", out, errOut)
	}
}

func TestWebAddDryRunAsksAndSaysSo(t *testing.T) {
	fake := &fakeAPI{}
	out, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "--target", "http://x:80", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !fake.lastWebDryRun || !strings.Contains(strings.ToLower(out), "dry run") {
		t.Fatalf("dry=%v out=%q", fake.lastWebDryRun, out)
	}
}

func TestWebAddShowsWhyTheServerRefused(t *testing.T) {
	fake := &fakeAPI{webErr: &api.APIError{StatusCode: 400, Reason: "validation", Errors: map[string][]string{"target": {"10.9.9.9 is not the address of a host of yours"}}}}
	_, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "--target", "http://10.9.9.9:80")
	if err == nil || !strings.Contains(err.Error(), "host of yours") {
		t.Fatalf("%v", err)
	}
}

func TestWebListShowsStateAndReason(t *testing.T) {
	fake := &fakeAPI{webSitesSeq: [][]api.WebSite{{
		site("api.example.org", uri("u1", "applied", "")),
		site("example.org", uri("u2", "errored", "proxy upstream not allowed by the local catalog")),
	}}}
	out, _, err := runWeb(t, fake, "domain", "example.org", "web", "list", "--server", "maple")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"api.example.org", "applied", "errored", "proxy upstream not allowed by the local catalog"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not in %q", want, out)
		}
	}
	if fake.lastWebServer != "maple" || fake.lastWebHost != "" {
		t.Fatalf("%q %q", fake.lastWebServer, fake.lastWebHost)
	}
}

func TestWebListCanBeLimitedToANameAndPrintedAsJSON(t *testing.T) {
	fake := &fakeAPI{webSitesSeq: [][]api.WebSite{{site("api.example.org", uri("u1", "pending", ""))}}}
	out, _, err := runWeb(t, fake, "domain", "example.org", "web", "list", "api", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var got []api.WebSite
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 || fake.lastWebHost != "api" {
		t.Fatalf("%v %q host=%q", err, out, fake.lastWebHost)
	}
}

func TestWebListSaysWhenThereIsNothing(t *testing.T) {
	out, _, err := runWeb(t, &fakeAPI{}, "domain", "example.org", "web", "list")
	if err != nil || !strings.Contains(out, "No web configuration") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestWebUpdateSendsOnlyWhatChanged(t *testing.T) {
	fake := &fakeAPI{}
	if _, _, err := runWeb(t, fake, "domain", "example.org", "web", "update", "u1", "--tls", "disabled"); err != nil {
		t.Fatal(err)
	}
	if fake.lastWebID != "u1" || len(fake.lastWebFields) != 1 || fake.lastWebFields["tls"] != "disabled" {
		t.Fatalf("%q %v", fake.lastWebID, fake.lastWebFields)
	}
	if _, _, err := runWeb(t, &fakeAPI{}, "domain", "example.org", "web", "update", "u1"); err == nil {
		t.Fatal("an update that changes nothing should be refused")
	}
}

func TestWebRemoveAndRestore(t *testing.T) {
	fake := &fakeAPI{}
	out, _, err := runWeb(t, fake, "domain", "example.org", "web", "remove", "u1")
	if err != nil || fake.lastWebID != "u1" || !strings.Contains(out, "Removed") {
		t.Fatalf("%v %q", err, out)
	}
	if _, _, err := runWeb(t, fake, "domain", "example.org", "web", "remove", "u1", "--dry-run"); err != nil || !fake.lastWebDryRun {
		t.Fatalf("%v dry=%v", err, fake.lastWebDryRun)
	}
	out, _, err = runWeb(t, fake, "domain", "example.org", "web", "restore", "u1")
	if err != nil || !strings.Contains(out, "u1") || fake.webCalls[len(fake.webCalls)-1] != "restore" {
		t.Fatalf("%v %q %v", err, out, fake.webCalls)
	}
}

func TestWebServersListsTheOnesIcanUse(t *testing.T) {
	fake := &fakeAPI{webServers: []api.WebServer{{ID: "w1", Name: "maple", Hostname: "maple.altenwald.com"}}}
	out, _, err := runWeb(t, fake, "web", "servers")
	if err != nil || !strings.Contains(out, "maple") || !strings.Contains(out, "maple.altenwald.com") {
		t.Fatalf("%v %q", err, out)
	}
	out, _, _ = runWeb(t, &fakeAPI{}, "web", "servers")
	if !strings.Contains(out, "No web servers") {
		t.Fatalf("%q", out)
	}
}

func fastPolling(t *testing.T) {
	t.Helper()
	old := webPollInterval
	webPollInterval = time.Millisecond
	t.Cleanup(func() { webPollInterval = old })
}

func TestWebWaitFollowsTheURIUntilTheServerAppliedIt(t *testing.T) {
	fastPolling(t)
	fake := &fakeAPI{webSitesSeq: [][]api.WebSite{
		{site("api.example.org", uri("u1", "pending", ""))},
		{site("api.example.org", uri("u1", "pending", ""))},
		{site("api.example.org", uri("u1", "applied", ""))},
	}}
	out, errOut, err := runWeb(t, fake, "domain", "example.org", "web", "add", "api", "--target", "http://x:80", "--wait")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "applied") || !strings.Contains(errOut, "pending") {
		t.Fatalf("out=%q err=%q", out, errOut)
	}
	if fake.lastWebHost != "api" {
		t.Fatalf("it should look at the name it changed: %q", fake.lastWebHost)
	}
}

func TestWebWaitFailsWithTheReasonTheServerGave(t *testing.T) {
	fastPolling(t)
	fake := &fakeAPI{webSitesSeq: [][]api.WebSite{{site("api.example.org", uri("u1", "errored", "proxy upstream not allowed by the local catalog"))}}}
	_, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "api", "--target", "http://x:80", "--wait")
	if err == nil || !strings.Contains(err.Error(), "proxy upstream not allowed by the local catalog") {
		t.Fatalf("%v", err)
	}
}

func TestWebWaitGivesUpAfterItsTimeout(t *testing.T) {
	fastPolling(t)
	fake := &fakeAPI{webSitesSeq: [][]api.WebSite{{site("api.example.org", uri("u1", "pending", ""))}}}
	_, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "api", "--target", "http://x:80", "--wait", "--timeout", "30ms")
	if err == nil || !strings.Contains(err.Error(), "still pending") {
		t.Fatalf("%v", err)
	}
}

func TestWebWaitIsNotUsedByADryRun(t *testing.T) {
	fake := &fakeAPI{}
	if _, _, err := runWeb(t, fake, "domain", "example.org", "web", "add", "--target", "http://x:80", "--dry-run", "--wait"); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.webCalls {
		if call == "list" {
			t.Fatal("nothing was written: there is nothing to wait for")
		}
	}
}

func TestWebErrorsKeepTheLoginHint(t *testing.T) {
	fake := &fakeAPI{webSitesErr: &api.APIError{StatusCode: 401, Reason: "authentication failed"}}
	_, _, err := runWeb(t, fake, "domain", "example.org", "web", "list")
	if err == nil || !strings.Contains(err.Error(), "dym auth login") {
		t.Fatalf("%v", err)
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the API error should stay in the chain")
	}
}
