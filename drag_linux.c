//go:build linux

// WebKitGTK fires no dragend for a drag GTK cancels: one dropped where
// nothing takes it, or ended with Escape. An emission hook on GdkDrag's
// "cancel" signal sees every drag in the process, so Go can tell the page.

#include <gtk/gtk.h>

extern void goDragCancelled(int reason);

static gboolean on_drag_cancel(GSignalInvocationHint *hint, guint n, const GValue *params, gpointer data) {
    // params[0] is the GdkDrag, params[1] its GdkDragCancelReason.
    goDragCancelled(n > 1 ? g_value_get_enum(&params[1]) : GDK_DRAG_CANCEL_ERROR);
    return TRUE; // keep the hook
}

void watchDragCancel(void) {
    g_type_class_ref(GDK_TYPE_DRAG); // the signal exists once the class does
    g_signal_add_emission_hook(g_signal_lookup("cancel", GDK_TYPE_DRAG), 0, on_drag_cancel, NULL, NULL);
}
