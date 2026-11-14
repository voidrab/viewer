package main

import (
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"

	"viewer/internal/ui/portal"
)

func TestParseResponse(t *testing.T) {
	uris := map[string]dbus.Variant{"uris": dbus.MakeVariant([]string{"file:///a.pdf", "file:///b.pdf"})}
	for name, tc := range map[string]struct {
		body []any
		want portal.Response
		ok   bool
	}{
		"success":       {[]any{uint32(0), uris}, portal.Response{Code: 0, URIs: []string{"file:///a.pdf", "file:///b.pdf"}}, true},
		"cancelled":     {[]any{uint32(1), map[string]dbus.Variant{}}, portal.Response{Code: 1}, true},
		"no uris":       {[]any{uint32(0), map[string]dbus.Variant{"other": dbus.MakeVariant("x")}}, portal.Response{}, true},
		"results wrong": {[]any{uint32(2), "not a map"}, portal.Response{Code: 2}, true},
		"short body":    {[]any{uint32(0)}, portal.Response{}, false},
		"code wrong":    {[]any{"0", uris}, portal.Response{}, false},
		"empty":         {nil, portal.Response{}, false},
	} {
		got, ok := parseResponse(tc.body)
		if ok != tc.ok || (ok && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("%s: parseResponse = %+v, %v; want %+v, %v", name, got, ok, tc.want, tc.ok)
		}
	}
}
