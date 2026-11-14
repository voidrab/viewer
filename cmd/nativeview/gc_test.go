package main

import (
	"runtime/debug"
	"testing"
)

func TestTuneGCSetsTheTargetOnlyWhenTheEnvironmentSetsNone(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
	}{{"", gcPercent}, {"GOGC", 100}, {"GOMEMLIMIT", 100}} {
		t.Setenv("GOGC", "")
		t.Setenv("GOMEMLIMIT", "")
		if tc.env != "" {
			t.Setenv(tc.env, "100MiB")
		}
		before := debug.SetGCPercent(100)
		tuneGC()
		if got := debug.SetGCPercent(before); got != tc.want {
			t.Errorf("with %q set: GC percent = %d, want %d", tc.env, got, tc.want)
		}
	}
}
