package main

import "runtime/debug"

// version is the program's version, set when building with
// -ldflags "-X main.version=…"; the Makefile sets it from git describe.
var version string

// readBuildInfo is debug.ReadBuildInfo; tests replace it.
var readBuildInfo = debug.ReadBuildInfo

// appVersion returns the version to show: the one set at build time, else the
// module version go install recorded, else the commit built from, marked
// -dirty when the tree had changes. It returns "" when none is known.
func appVersion() string {
	if version != "" {
		return version
	}
	info, ok := readBuildInfo()
	if !ok {
		return ""
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
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
