//go:build linux

package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#ifdef __GLIBC__
#include <malloc.h>
static void trim_heap(gpointer data, GObject *gone) { malloc_trim(0); }
// Runs before main, ahead of any thread: glibc gives each thread that
// contends for the heap its own arena, up to 8 per core, and memory freed
// into one is rarely reused by another. GTK and WebKit run many threads,
// so the process keeps growing as windows open and close. Two arenas keep
// freed memory reusable at no cost to a viewer's light allocation load.
__attribute__((constructor)) static void limit_arenas(void) { mallopt(M_ARENA_MAX, 2); }
#else
static void trim_heap(gpointer data, GObject *gone) {}
#endif

// Drops Wails's reference to window, and returns the memory the window
// freed to the system once it is finalized: glibc keeps freed memory in its
// arenas, so without a trim the process never shrinks after a window closes.
static void release_window(gpointer window) {
    g_object_weak_ref(G_OBJECT(window), trim_heap, NULL);
    g_object_unref(window);
}
*/
import "C"

import "unsafe"

// releaseWindowRef drops the reference Wails v3.1.0 takes on each GtkWindow
// it creates (g_object_ref_sink in windowNew) and never releases. Without
// this, a closed window is never finalized: its WebKitWebView, and the web
// process behind it, stay alive for the rest of the run. GTK's own reference
// keeps the window alive until Wails destroys it, which then frees it all;
// after that, Wails uses the pointer only as a lookup key. Once the window
// is finalized, the heap is trimmed. Call it once per window, on the main
// thread.
func releaseWindowRef(native unsafe.Pointer) {
	if native != nil {
		C.release_window(C.gpointer(native))
	}
}
