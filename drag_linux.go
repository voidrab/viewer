//go:build linux

package main

/*
#cgo pkg-config: gtk4
#include <gdk/gdk.h>
void watchDragCancel(void);
*/
import "C"

// onDragCancel is called for each drag that GTK cancels. It is set by
// watchDragCancels.
var onDragCancel func(byUser bool)

// watchDragCancels calls f whenever GTK cancels a drag, which WebKitGTK does
// not report to the page; byUser tells an Escape from a drop nothing took.
func watchDragCancels(f func(byUser bool)) {
	onDragCancel = f
	C.watchDragCancel()
}

//export goDragCancelled
func goDragCancelled(reason C.int) {
	onDragCancel(reason == C.GDK_DRAG_CANCEL_USER_CANCELLED)
}
