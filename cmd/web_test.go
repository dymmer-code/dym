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

func TestWebOptionsWithoutFlagsShowsThem(t *testing.T) {
	fake := &fakeAPI{webOptions: &api.WebOptions{Status: "ok", Host: "games.altenwald.com", Options: map[string]any{"blog_fallback": true, "asset_extensions": "jpg|css"}}}
	out, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "options", "games")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.webCalls) != 1 || fake.webCalls[0] != "options-get" || fake.lastWebHost != "games" {
		t.Fatalf("calls %v host %q", fake.webCalls, fake.lastWebHost)
	}
	for _, want := range []string{"blog_fallback", "true", "asset_extensions", "jpg|css"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestWebOptionsWithNoneSaysSo(t *testing.T) {
	out, _, err := runWeb(t, &fakeAPI{}, "domain", "altenwald.com", "web", "options")
	if err != nil || !strings.Contains(out, "has no options") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestWebOptionsSendsOnlyTheOnesGivenAndFalseTakesThemAway(t *testing.T) {
	fake := &fakeAPI{}
	_, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "options", "games", "--hsts", "--blog-fallback=false", "--asset-extensions", "jpg|css")
	if err != nil {
		t.Fatal(err)
	}
	got := fake.lastWebOpts
	if len(got) != 3 || got["hsts"] != true || got["blog_fallback"] != nil || got["asset_extensions"] != "jpg|css" {
		t.Fatalf("options = %v", got)
	}
	if _, present := got["blog_fallback"]; !present {
		t.Fatalf("a nil must be sent to take the option away: %v", got)
	}
}

func TestWebOptionsEmptyExtensionsTakesThemAway(t *testing.T) {
	fake := &fakeAPI{}
	if _, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "options", "--asset-extensions", ""); err != nil {
		t.Fatal(err)
	}
	if v, present := fake.lastWebOpts["asset_extensions"]; !present || v != nil {
		t.Fatalf("options = %v", fake.lastWebOpts)
	}
}

func TestWebOptionsDryRunSaysSo(t *testing.T) {
	fake := &fakeAPI{}
	out, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "options", "--hsts", "--dry-run")
	if err != nil || !fake.lastWebDryRun || !strings.Contains(out, "Dry run") {
		t.Fatalf("%v %v %q", err, fake.lastWebDryRun, out)
	}
}

func TestWebOptionsShowsTheReasonOfARefusal(t *testing.T) {
	fake := &fakeAPI{webErr: errors.New("dymmer api: validation (status 400) [hsts: needs every URI of the name with tls required]")}
	_, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "options", "--hsts")
	if err == nil || !strings.Contains(err.Error(), "needs every URI") {
		t.Fatalf("err = %v", err)
	}
}

func TestWebAddARedirectSendsItsCode(t *testing.T) {
	fake := &fakeAPI{}
	_, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "add", "old", "--type", "redirect", "--target", "https://new.example.org", "--status", "308")
	if err != nil {
		t.Fatal(err)
	}
	f := fake.lastWebFields
	if f["type"] != "redirect" || f["redirect_status"] != "308" || f["target"] != "https://new.example.org" {
		t.Fatalf("fields = %v", f)
	}
}

func TestWebStatusOnlyAcceptsTheFourCodes(t *testing.T) {
	for _, code := range []string{"200", "303", "0", "-1"} {
		_, _, err := runWeb(t, &fakeAPI{}, "domain", "altenwald.com", "web", "add", "old", "--type", "redirect", "--target", "https://x.example.org", "--status", code)
		if err == nil || !strings.Contains(err.Error(), "301, 302, 307 or 308") {
			t.Fatalf("status %s: err = %v", code, err)
		}
	}
}

func TestWebUpdateCanChangeOnlyTheCode(t *testing.T) {
	fake := &fakeAPI{}
	if _, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "update", "u1", "--status", "301"); err != nil {
		t.Fatal(err)
	}
	if len(fake.lastWebFields) != 1 || fake.lastWebFields["redirect_status"] != "301" {
		t.Fatalf("fields = %v", fake.lastWebFields)
	}
}

func TestWebListShowsTheCodeNextToTheType(t *testing.T) {
	redirect := api.WebURI{ID: "r1", URI: "/", Type: "redirect", Target: "https://new.example.org", TLS: "required", State: "applied", RedirectStatus: 301}
	fake := &fakeAPI{webSitesSeq: [][]api.WebSite{{site("old.altenwald.com", redirect)}}}
	out, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "list")
	if err != nil || !strings.Contains(out, "redirect 301") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestWebOptionsRetiredLandingAndItsMessage(t *testing.T) {
	fake := &fakeAPI{}
	_, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "options", "cuatro", "--retired-landing", "--retired-message", "Esta demo ha finalizado.")
	if err != nil {
		t.Fatal(err)
	}
	got := fake.lastWebOpts
	if len(got) != 2 || got["retired_landing"] != true || got["retired_message"] != "Esta demo ha finalizado." {
		t.Fatalf("options = %v", got)
	}
}

func TestWebOptionsRetiredLandingFalseAndEmptyMessageTakeThemAway(t *testing.T) {
	fake := &fakeAPI{}
	if _, _, err := runWeb(t, fake, "domain", "altenwald.com", "web", "options", "--retired-landing=false", "--retired-message", ""); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"retired_landing", "retired_message"} {
		if v, present := fake.lastWebOpts[key]; !present || v != nil {
			t.Fatalf("%s must be sent as nil: %v", key, fake.lastWebOpts)
		}
	}
}

func TestWebBucketsListsTheNamesOrSaysThereAreNone(t *testing.T) {
	out, _, err := runWeb(t, &fakeAPI{webBuckets: []api.WebBucket{{Name: "altenwald-org"}, {Name: "games-altenwald-com"}}}, "web", "buckets")
	if err != nil || !strings.Contains(out, "BUCKET") || !strings.Contains(out, "altenwald-org") || !strings.Contains(out, "games-altenwald-com") {
		t.Fatalf("%v %q", err, out)
	}
	out, _, err = runWeb(t, &fakeAPI{}, "web", "buckets")
	if err != nil || !strings.Contains(out, "No buckets found") {
		t.Fatalf("%v %q", err, out)
	}
	out, _, err = runWeb(t, &fakeAPI{webBuckets: []api.WebBucket{{Name: "x-y"}}}, "web", "buckets", "-o", "json")
	if err != nil || !strings.Contains(out, `"name":"x-y"`) {
		t.Fatalf("%v %q", err, out)
	}
}
