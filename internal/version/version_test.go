package version

import "testing"

func TestUserAgentHasTheVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "1.2.3"
	if got := UserAgent(); got != "dym/1.2.3" {
		t.Fatalf("got %q", got)
	}

	Version = "v0.15.0"
	if got := UserAgent(); got != "dym/0.15.0" {
		t.Fatalf("a leading v is dropped, got %q", got)
	}
}

func TestAnUnstampedBuildSaysDev(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "dev"
	if got := String(); got == "" {
		t.Fatal("the version is never empty")
	}
}
