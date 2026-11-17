package app

import (
	"image"
	"image/color"
	"testing"
	"time"

	"viewrab/internal/ui/pageview"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/theme"
	"viewrab/internal/ui/widget"
)

// typeKeys types s, rune by rune.
func (f *fixture) typeKeys(s string) {
	for _, r := range s {
		f.key(r, 0)
	}
}

func TestTypingAPageNumberGoesToThatPage(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 9))
	f.a.Handle(platform.FrameEvent{})
	view := f.a.current().view
	tb := f.a.toolbar

	// Clicking the field selects the page number, so typing replaces it, and
	// Enter goes to the page typed. Letters are not taken.
	f.clickButton(widget.ButtonPage)
	if !tb.EditingPage || tb.PageField.String() != "1" || !tb.PageField.All {
		t.Fatalf("editing %v, field %q, all %v", tb.EditingPage, tb.PageField.String(), tb.PageField.All)
	}
	f.typeKeys("x5")
	f.special(platform.KeyEnter, 0)
	if tb.EditingPage || view.Page() != 4 {
		t.Errorf("after 5 Enter: editing %v, page %d", tb.EditingPage, view.Page()+1)
	}
	// A number past the end goes to the last page, one before the start to
	// the first.
	for _, tc := range []struct {
		typed string
		want  int
	}{{"99", 9}, {"0", 1}} {
		f.clickButton(widget.ButtonPage)
		f.typeKeys(tc.typed)
		f.special(platform.KeyEnter, 0)
		if got := view.Page() + 1; got != tc.want {
			t.Errorf("typed %s: page %d, want %d", tc.typed, got, tc.want)
		}
	}
	// Escape leaves the field without going anywhere, and Enter on an empty
	// field does the same.
	f.clickButton(widget.ButtonPage)
	f.typeKeys("7")
	f.special(platform.KeyEscape, 0)
	if tb.EditingPage || view.Page() != 0 {
		t.Errorf("after Escape: editing %v, page %d", tb.EditingPage, view.Page()+1)
	}
	f.clickButton(widget.ButtonPage)
	f.special(platform.KeyBackspace, 0)
	f.special(platform.KeyEnter, 0)
	if view.Page() != 0 {
		t.Errorf("an empty field went to page %d", view.Page()+1)
	}
}

func TestThePageFieldKeepsTheKeysUntilItLosesTheFocus(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 9))
	f.a.Handle(platform.FrameEvent{})
	view := f.a.current().view
	tb := f.a.toolbar

	f.clickButton(widget.ButtonPage)
	// The arrows move the caret, not the pages; Ctrl+A selects the field.
	f.special(platform.KeyRight, 0)
	f.special(platform.KeyDown, 0)
	if view.Page() != 0 || tb.PageField.All {
		t.Errorf("keys reached the view: page %d", view.Page()+1)
	}
	f.key('a', platform.Ctrl)
	if !tb.PageField.All {
		t.Error("Ctrl+A did not select the field")
	}
	// A click in the field moves the caret and keeps the field.
	r := tb.ButtonRect(widget.ButtonPage)
	f.click(r.Min.X+2, (r.Min.Y+r.Max.Y)/2)
	if !tb.EditingPage || tb.PageField.All || tb.PageField.Cursor != 0 {
		t.Errorf("click in the field: editing %v, all %v, caret %d", tb.EditingPage, tb.PageField.All, tb.PageField.Cursor)
	}
	// A press anywhere else ends the field, going to the page typed, as an
	// input does when it loses the focus.
	f.key('a', platform.Ctrl)
	f.typeKeys("3")
	c := f.content()
	f.click(c.Min.X+5, c.Min.Y+5)
	if tb.EditingPage || view.Page() != 2 {
		t.Errorf("after a click on the page: editing %v, page %d", tb.EditingPage, view.Page()+1)
	}
	// Ctrl shortcuts still work; Ctrl+F moves the keys to the find field.
	f.clickButton(widget.ButtonPage)
	f.key('f', platform.Ctrl)
	if tb.EditingPage || !f.a.findBar.Open {
		t.Errorf("Ctrl+F: editing %v, find open %v", tb.EditingPage, f.a.findBar.Open)
	}
}

func TestThePageFieldBelongsToItsTab(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 9), f.pdf("b.pdf", 2))
	f.a.Handle(platform.FrameEvent{})
	f.clickButton(widget.ButtonPage)
	f.typeKeys("2")
	// Switching tabs drops what was typed.
	f.a.selectTab(0)
	f.a.Handle(platform.FrameEvent{})
	if f.a.toolbar.EditingPage || f.a.docs[0].view.Page() != 0 || f.a.docs[1].view.Page() != 0 {
		t.Error("the page typed outlived its tab")
	}
	// Closing the last tab ends the edit too.
	f.clickButton(widget.ButtonPage)
	f.key('w', platform.Ctrl)
	f.key('w', platform.Ctrl)
	f.a.Handle(platform.FrameEvent{})
	if f.a.toolbar.EditingPage || f.a.pageDoc != nil {
		t.Error("the page field is still being edited with no document")
	}
}

func TestTheZoomDropDownOffersThePresets(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 3))
	f.a.Handle(platform.FrameEvent{})
	view := f.a.current().view

	f.clickButton(widget.ButtonZoomMode)
	m := f.a.menu
	f.a.Handle(platform.FrameEvent{})
	if !m.Open || len(m.Items) != len(pageview.ZoomPresets) || m.Items[m.Checked] != pageview.DefaultZoom.Label || !f.a.toolbar.On[widget.ButtonZoomMode] {
		t.Fatalf("menu open %v, items %v, checked %d", m.Open, m.Items, m.Checked)
	}
	// Clicking an item applies it.
	r := m.Rect()
	row := func(i int) (int, int) { return r.Min.X + 20, r.Min.Y + 4 + i*26 + 13 }
	x, y := row(4) // 100%
	f.click(x, y)
	if m.Open || view.ZoomLabel() != "100%" {
		t.Errorf("after choosing 100%%: open %v, zoom %q", m.Open, view.ZoomLabel())
	}
	// The keys choose too, and the menu has the keys while it is open.
	f.clickButton(widget.ButtonZoomMode)
	f.special(platform.KeyUp, 0)
	f.special(platform.KeyEnter, 0)
	if view.ZoomLabel() != "75%" {
		t.Errorf("Up Enter from 100%%: %q", view.ZoomLabel())
	}
	// A click elsewhere closes it, choosing nothing, and reaches nothing.
	f.clickButton(widget.ButtonZoomMode)
	f.clickButton(widget.ButtonOpen)
	if m.Open || view.ZoomLabel() != "75%" || f.a.message != "" {
		t.Errorf("a click outside: open %v, zoom %q, message %q", m.Open, view.ZoomLabel(), f.a.message)
	}
	// A choice left over once the document is gone does nothing.
	f.a.showMenu(menuZoom, []string{"fit width"}, 0, image.Rect(0, 0, 10, 10))
	f.key('w', platform.Ctrl)
	f.a.choose(0)
	f.a.choose(5) // not an item
}

func TestTheProfileDropDownChoosesTheProfile(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	f.a.setPanel(true)
	f.panelClick(widget.PanelAction{Kind: widget.PanelProfiles})
	m := f.a.menu
	if !m.Open || len(m.Items) != len(f.a.profiles) || m.Checked != 0 {
		t.Fatalf("menu open %v, items %v, checked %d", m.Open, m.Items, m.Checked)
	}
	// It drops from the drop-down, and the zoom drop-down is not shown as open.
	pr := f.a.panelRect()
	drop := f.a.panel.ItemRect(widget.PanelAction{Kind: widget.PanelProfiles}, pr.Dx()).Add(pr.Min)
	if r := m.Rect(); r.Min.Y < drop.Max.Y || r.Max.X > winW {
		t.Errorf("menu at %v below the drop-down at %v", r, drop)
	}
	f.a.Handle(platform.FrameEvent{})
	if f.a.toolbar.On[widget.ButtonZoomMode] {
		t.Error("the zoom drop-down looks open")
	}
	f.special(platform.KeyDown, 0)
	f.special(platform.KeyEnter, 0)
	if f.a.panel.Profile != 1 {
		t.Errorf("profile %d, want 1", f.a.panel.Profile)
	}
}

func TestTheThemeSwitch(t *testing.T) {
	f := newFixture(t)
	var saved []bool
	f.a.opts.SaveTheme = func(dark bool) { saved = append(saved, dark) }
	f.open(f.pdf("a.pdf", 1))
	// The window starts light. The switch makes it dark everywhere and
	// passes the choice to SaveTheme.
	f.clickButton(widget.ButtonTheme)
	dark := theme.Dark()
	if !f.a.opts.Palette.Dark || f.a.toolbar.Palette != dark || f.a.tabs.Palette != dark || f.a.status.Palette != dark ||
		f.a.findBar.Palette != dark || f.a.panel.Palette != dark || f.a.menu.Palette != dark {
		t.Error("the switch did not make every part dark")
	}
	if len(saved) != 1 || !saved[0] {
		t.Errorf("saved %v", saved)
	}
	f.a.Handle(platform.FrameEvent{})
	img := f.win.Presented()[len(f.win.Presented())-1]
	c := f.content()
	if got := img.RGBAAt(c.Min.X+2, c.Min.Y+2); got != nrgbaOf(dark.Canvas) {
		t.Errorf("backdrop %v, want the dark canvas", got)
	}
	// Without SaveTheme, switching back still works.
	f.a.opts.SaveTheme = nil
	f.clickButton(widget.ButtonTheme)
	if f.a.opts.Palette.Dark {
		t.Error("the switch did not go back to light")
	}
	// Dark set in the options starts dark.
	g := newFixture(t)
	if g.a.opts.Palette.Dark {
		t.Error("a window without Dark starts dark")
	}
	h, err := New(g.win, Options{Dark: true})
	if err != nil || !h.opts.Palette.Dark {
		t.Errorf("New with Dark: %v, dark %v", err, err == nil && h.opts.Palette.Dark)
	}
}

func TestTheEmptyStatesButtonOpensAFile(t *testing.T) {
	f := newFixture(t)
	f.a.opts.PickFiles = func() ([]string, error) { return nil, nil }
	f.start()
	f.a.Handle(platform.FrameEvent{})
	_, _, _, button := f.a.hintLayout(f.content())
	x, y := mid(button)
	f.pointer(platform.Move, platform.None, x, y)
	if !f.a.hintHover {
		t.Error("the button is not lit under the pointer")
	}
	f.a.Handle(platform.FrameEvent{}) // drawn lit
	f.pointer(platform.Leave, platform.None, 0, 0)
	if f.a.hintHover {
		t.Error("the button stayed lit after the pointer left")
	}
	f.click(x, y)
	if !f.a.picking {
		t.Error("clicking the button did not ask for a file")
	}
	f.drain()
}

func TestRunFollowsTheDesktopTheme(t *testing.T) {
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
	fw := w.(*platform.FakeWindow)
	themes := make(chan bool)
	done := make(chan error, 1)
	go func() { done <- Run(fw, nil, Options{Themes: themes, Now: time.Now}) }()
	themes <- true
	waitFor(t, "a dark frame", func() bool {
		shots := fw.Presented()
		if len(shots) == 0 {
			return false
		}
		return shots[len(shots)-1].RGBAAt(5, winH/2) == nrgbaOf(theme.Dark().Canvas)
	})
	fw.Send(platform.CloseEvent{})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunIgnoresAClosedThemeChannel(t *testing.T) {
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
	fw := w.(*platform.FakeWindow)
	// With the window's first events taken, the closed channel is the only
	// thing Run can read, so it reads it before the close below.
	<-fw.Events()
	<-fw.Events()
	themes := make(chan bool)
	close(themes)
	done := make(chan error, 1)
	go func() { done <- Run(fw, nil, Options{Themes: themes}) }()
	fw.Send(platform.CloseEvent{})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// nrgbaOf returns the opaque colour c as the canvas stores it.
func nrgbaOf(c color.NRGBA) color.RGBA { return color.RGBA{c.R, c.G, c.B, 255} }
