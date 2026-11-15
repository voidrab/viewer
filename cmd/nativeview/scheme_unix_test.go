//go:build !windows

package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestSchemeIsDark(t *testing.T) {
	for _, tc := range []struct {
		v        any
		dark, ok bool
	}{
		{uint32(1), true, true},
		{uint32(2), false, true},
		{uint32(0), false, true},
		{dbus.MakeVariant(uint32(1)), true, true},
		{dbus.MakeVariant(dbus.MakeVariant(uint32(1))), true, true},
		{dbus.MakeVariant("dark"), false, false},
		{nil, false, false},
	} {
		if dark, ok := schemeIsDark(tc.v); dark != tc.dark || ok != tc.ok {
			t.Errorf("schemeIsDark(%v) = %v, %v", tc.v, dark, ok)
		}
	}
}
