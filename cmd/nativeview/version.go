package main

import (
	"regexp"
	"runtime/debug"
)

// version is the program's version. The build sets it with
// -ldflags "-X main.version=…".
var version string

// readBuildInfo is debug.ReadBuildInfo; tests replace it.
var readBuildInfo = debug.ReadBuildInfo

// pseudoVersion matches the version go build stamps from a commit that no tag
// names, such as v0.0.0-20261115160453-f2847983d7e0+dirty.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}(\+dirty)?$`)

// appVersion returns the version to show: the one set at build time, else the
// module version go install or go build recorded, unless it is a
// pseudo-version, else the commit built from, marked -dirty when the tree had
// changes. It returns "" when none is known.
func appVersion() string {
	if version != "" {
		return version
	}
	info, ok := readBuildInfo()
	if !ok {
		return ""
	}
	if v := info.Main.Version; v != "" && v != "(devel)" && !pseudoVersion.MatchString(v) {
		return v
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision":
			rev = s.Value[:min(len(s.Value), 7)]
		case s.Key == "vcs.modified" && s.Value == "true":
			dirty = "-dirty"
		}
	}
	if rev == "" {
		return ""
	}
	return rev + dirty
}
