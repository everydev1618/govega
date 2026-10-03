package main

import "runtime/debug"

// resolveVersion reports the version to print and serve.
//
// GoReleaser stamps ldflags via -X main.version, so release binaries are
// authoritative. Binaries from `go install ...@latest` carry no ldflags but do
// carry the module version in their build info, which is otherwise reported as
// a bare "dev".
func resolveVersion(ldflags string, info *debug.BuildInfo, ok bool) string {
	if ldflags != "dev" && ldflags != "" {
		return ldflags
	}
	// "(devel)" is what a build from a working tree reports — not a version.
	if ok && info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
