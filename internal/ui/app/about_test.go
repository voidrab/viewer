package app

import (
	"testing"

	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/theme"
	"viewrab/internal/ui/widget"
)

// openAbout opens the About dialog from the application menu.
func (f *fixture) openAbout() {
	f.t.Helper()
	f.clickButton(widget.ButtonMenu)
	f.a.Handle(platform.FrameEvent{})
	if !f.a.menu.Open || len(f.a.menu.Items) != 1 || !f.a.toolbar.On[widget.ButtonMenu] {
		f.t.Fatalf("menu open %v, items %v", f.a.menu.Open, f.a.menu.Items)
	}
	// Nothing is highlighted at first; Down then Enter chooses the item.
	f.special(platform.KeyDown, 0)
	f.special(platform.KeyEnter, 0)
	if !f.a.about.Open {
		f.t.Fatal("choosing About did not open the dialog")
	}
}

func TestTheAboutDialog(t *testing.T) {
	f := newFixture(t)
	var opened []string
	f.a.opts.OpenURI = func(uri string) { opened = append(opened, uri) }
	f.a.about.Version = "v1.2.0"
	// The menu and the dialog work with no document open.
	f.start()
	f.openAbout()
	f.a.Handle(platform.FrameEvent{})
	if got := f.win.Presented()[len(f.win.Presented())-1].RGBAAt(2, winH-2); got == nrgbaOf(f.a.opts.Palette.Chrome) {
		t.Error("the window behind the dialog is not dimmed")
	}

	// A link is opened with OpenURI, and the pointer is a hand over it.
	card := f.a.about.Rect(f.a.bounds())
	r := f.a.about.LinkRect(widget.WebsiteURL, f.a.bounds())
	link := r.Min.Add(r.Max).Div(2)
	f.pointer(platform.Move, platform.None, link.X, link.Y)
	if f.win.Cursor() != platform.CursorPointer {
		t.Errorf("cursor %v over a link", f.win.Cursor())
	}
	f.click(link.X, link.Y)
	if len(opened) != 1 || opened[0] != widget.WebsiteURL || !f.a.about.Open {
		t.Errorf("opened %v, dialog open %v", opened, f.a.about.Open)
	}
	// Without OpenURI a link click does nothing.
	f.a.opts.OpenURI = nil
	f.click(link.X, link.Y)
	f.pointer(platform.Move, platform.None, card.Min.X+2, card.Min.Y+2)
	if f.win.Cursor() != platform.CursorDefault {
		t.Errorf("cursor %v off the links", f.win.Cursor())
	}

	// While the dialog is open, shortcuts do not reach the window behind it:
	// Ctrl+O opens no file dialog.
	f.key('o', platform.Ctrl)
	if len(f.picks) != 0 {
		t.Error("a shortcut reached the window behind the dialog")
	}
	// A theme change applies to the dialog too.
	f.a.setTheme(true)
	if f.a.about.Palette != theme.Dark() {
		t.Error("the dialog kept the light palette")
	}
	// Escape closes it, and a click outside closes it without reaching the
	// toolbar under it.
	f.special(platform.KeyEscape, 0)
	if f.a.about.Open {
		t.Error("Escape left the dialog open")
	}
	f.openAbout()
	f.clickButton(widget.ButtonTheme)
	if f.a.about.Open || !f.a.opts.Palette.Dark {
		t.Errorf("a click outside: open %v, dark %v", f.a.about.Open, f.a.opts.Palette.Dark)
	}
}

func TestTheApplicationMenuOutlivesTheLastDocument(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	f.clickButton(widget.ButtonMenu)
	f.key('w', platform.Ctrl)
	f.a.Handle(platform.FrameEvent{})
	if !f.a.menu.Open {
		t.Error("closing the last tab closed the application menu")
	}
}
