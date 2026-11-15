package main

import (
	"runtime/debug"
	"testing"
)

func TestAppVersion(t *testing.T) {
	defer func(v string, r func() (*debug.BuildInfo, bool)) { version, readBuildInfo = v, r }(version, readBuildInfo)
	info := func(main string, settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: main}, Settings: settings}, true
		}
	}
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "3b48fe4c0ffee"}
	for _, c := range []struct {
		name, set string
		read      func() (*debug.BuildInfo, bool)
		want      string
	}{
		{"set at build time", "v1.2.0-3-g3b48fe4", info("(devel)"), "v1.2.0-3-g3b48fe4"},
		{"no build info", "", func() (*debug.BuildInfo, bool) { return nil, false }, ""},
		{"module version", "", info("v1.2.0", rev), "v1.2.0"},
		{"commit", "", info("(devel)", rev), "3b48fe4"},
		{"pseudo-version", "", info("v0.0.0-20261115160453-f2847983d7e0+dirty", rev), "3b48fe4"},
		{"pseudo-version after a tag", "", info("v1.2.1-0.20261115160453-f2847983d7e0", rev), "3b48fe4"},
		{"pseudo-version after a pre-release", "", info("v1.2.0-rc.1.0.20261115160453-f2847983d7e0", rev), "3b48fe4"},
		{"modified tree", "", info("(devel)", rev, debug.BuildSetting{Key: "vcs.modified", Value: "true"}), "3b48fe4-dirty"},
		{"clean tree", "", info("", rev, debug.BuildSetting{Key: "vcs.modified", Value: "false"}), "3b48fe4"},
		{"nothing known", "", info("(devel)"), ""},
	} {
		version, readBuildInfo = c.set, c.read
		if got := appVersion(); got != c.want {
			t.Errorf("%s: appVersion() = %q, want %q", c.name, got, c.want)
		}
	}
}
