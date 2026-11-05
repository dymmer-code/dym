// Package version says which dym this is. It is sent to Dymmer in the User-Agent of every request, so that Dymmer
// can tell which versions are still in use.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at build time (see .goreleaser.yml); a build that does not set it says "dev" unless it was
// installed with `go install`, which records the module version.
var Version = "dev"

// String is the version of this build, without the leading "v".
func String() string {
	if Version != "dev" {
		return strings.TrimPrefix(Version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

// UserAgent is the value of the User-Agent header of every request to Dymmer: dym/<version>.
func UserAgent() string { return "dym/" + String() }
