//go:build !windows

package main

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

// colorSchemeKey, in the org.freedesktop.appearance namespace of the settings
// portal, holds the desktop's colour scheme: 1 when the user prefers dark, 2
// when they prefer light, and 0 when they have no preference.
const (
	settingsIface  = "org.freedesktop.portal.Settings"
	appearance     = "org.freedesktop.appearance"
	colorSchemeKey = "color-scheme"
	settingChanged = settingsIface + ".SettingChanged"
	schemeDark     = 1
)

// schemeIsDark decodes a color-scheme value as the portal returns it, a
// variant holding a uint32 (or, from the older Read method, a variant inside
// a variant). It reports whether the scheme is dark, and whether the value had
// that shape.
func schemeIsDark(v any) (dark, ok bool) {
	for {
		inner, isVariant := v.(dbus.Variant)
		if !isVariant {
			break
		}
		v = inner.Value()
	}
	n, ok := v.(uint32)
	return n == schemeDark, ok
}

// readScheme asks the settings portal for the desktop's colour scheme. It
// reports whether the scheme is dark, and whether it could be read.
// It waits at most schemeTimeout, since the window does not open until then.
func readScheme(conn *dbus.Conn) (dark, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), schemeTimeout)
	defer cancel()
	obj := conn.Object(portalName, portalPath)
	var v dbus.Variant
	if err := obj.CallWithContext(ctx, settingsIface+".ReadOne", 0, appearance, colorSchemeKey).Store(&v); err != nil {
		// Portals before version 2 only have the deprecated Read.
		if err := obj.CallWithContext(ctx, settingsIface+".Read", 0, appearance, colorSchemeKey).Store(&v); err != nil {
			return false, false
		}
	}
	return schemeIsDark(v)
}

// schemeTimeout bounds the wait for the desktop's colour scheme at startup.
const schemeTimeout = 300 * time.Millisecond

// watchScheme reads the desktop's colour scheme into t and then follows its
// changes, sending the theme to show on out whenever the window should follow
// one. It keeps the session bus connection open for as long as the program
// runs. Without a session bus the theme is not followed; without a portal the
// desktop's preference is unknown, and the theme is light unless chosen.
func watchScheme(t *themes, out chan<- bool) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	t.setSystem(readScheme(conn))
	if conn.AddMatchSignal(
		dbus.WithMatchObjectPath(portalPath),
		dbus.WithMatchInterface(settingsIface),
		dbus.WithMatchMember("SettingChanged"),
		dbus.WithMatchArg(0, appearance),
		dbus.WithMatchArg(1, colorSchemeKey),
	) != nil {
		conn.Close()
		return
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	go func() {
		for sig := range signals {
			if sig.Name != settingChanged || len(sig.Body) != 3 {
				continue
			}
			if ns, _ := sig.Body[0].(string); ns != appearance {
				continue
			}
			if key, _ := sig.Body[1].(string); key != colorSchemeKey {
				continue
			}
			if dark, ok := schemeIsDark(sig.Body[2]); ok && t.systemChanged(dark) {
				out <- dark
			}
		}
	}()
}
