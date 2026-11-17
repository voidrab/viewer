package widget

import (
	"image"
	"testing"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
)

func newMenu(t *testing.T) *Menu {
	t.Helper()
	m := NewMenu(palette, newFind(t).Face)
	m.Items = []string{"fit width", "fit page", "100%"}
	m.Checked = 1
	return m
}

var (
	menuAnchor = image.Rect(200, 5, 300, 31)
	menuLimit  = image.Rect(0, 0, 900, 600)
)

func TestMenuOpensBelowItsAnchorWithinItsLimit(t *testing.T) {
	m := newMenu(t)
	if m.Open {
		t.Fatal("a new menu is open")
	}
	m.Show(menuAnchor, menuLimit)
	r := m.Rect()
	if !m.Open || r.Min.X != menuAnchor.Min.X || r.Min.Y <= menuAnchor.Max.Y || r.Dx() < menuAnchor.Dx() {
		t.Errorf("menu at %v below %v", r, menuAnchor)
	}
	if r.Dy() != 3*menuItem+2*menuPad {
		t.Errorf("menu is %d high", r.Dy())
	}
	// It starts with the checked item highlighted.
	if m.hover != 1 {
		t.Errorf("highlight %d", m.hover)
	}
	// Near the right or bottom edge it moves left or up to stay inside.
	m.Show(image.Rect(880, 580, 900, 590), menuLimit)
	if r := m.Rect(); !r.In(menuLimit) {
		t.Errorf("menu at %v leaves %v", r, menuLimit)
	}
	// A limit too small for it keeps its top-left corner inside.
	m.Show(image.Rect(10, 10, 20, 20), image.Rect(0, 0, 30, 30))
	if r := m.Rect(); r.Min.X < 0 || r.Min.Y < 0 {
		t.Errorf("menu at %v in a tiny limit", r)
	}
	m.Close()
	if m.Open || m.hover != -1 {
		t.Error("Close left the menu open")
	}
}

func TestMenuDraw(t *testing.T) {
	m := newMenu(t)
	c := paint.New(900, 600)
	c.Fill(c.Bounds(), palette.Canvas)
	m.Draw(c) // closed: nothing
	if inkShape(c, menuLimit, palette.Canvas) != 0 {
		t.Fatal("a closed menu drew")
	}
	m.Show(menuAnchor, menuLimit)
	m.hover = 2
	m.Draw(c)
	r := m.Rect()
	if got := c.Img.RGBAAt(r.Max.X-menuPad-1, menuRow(r, 0).Min.Y+2); got != nrgba(palette.Raised) {
		t.Errorf("menu background = %v", got)
	}
	// The checked item is drawn in the accent colour with its tick, the
	// others in the text colour; the highlighted one is washed.
	if inkIn(c, menuRow(r, 1), palette.Accent).Empty() {
		t.Error("the checked item is not in the accent colour")
	}
	if inkIn(c, menuRow(r, 0), palette.Text).Empty() {
		t.Error("an item is not in the text colour")
	}
	if got := c.Img.RGBAAt(menuRow(r, 2).Max.X-3, menuRow(r, 2).Min.Y+5); got == nrgba(palette.Raised) {
		t.Error("the highlighted item is not washed")
	}
}

func TestMenuPointer(t *testing.T) {
	m := newMenu(t)
	// Closed, it takes nothing.
	if i, taken := m.HandlePointer(ptr(platform.Press, platform.Left, 250, 50)); taken || i != -1 {
		t.Errorf("closed menu: %d, %v", i, taken)
	}
	m.Show(menuAnchor, menuLimit)
	r := m.Rect()
	x, y := mid(menuRow(r, 2))
	// Moving highlights; moving off the items keeps the highlight.
	if i, taken := m.HandlePointer(ptr(platform.Move, platform.None, x, y)); !taken || i != -1 || m.hover != 2 {
		t.Errorf("move: %d %v hover %d", i, taken, m.hover)
	}
	m.HandlePointer(ptr(platform.Move, platform.None, 5, 5))
	if m.hover != 2 {
		t.Errorf("hover after leaving the items = %d", m.hover)
	}
	// A press and release on an item chooses it and closes the menu.
	m.HandlePointer(ptr(platform.Press, platform.Left, x, y))
	if i, taken := m.HandlePointer(ptr(platform.Release, platform.Left, x, y)); !taken || i != 2 || m.Open {
		t.Errorf("click on item 2: %d %v open %v", i, taken, m.Open)
	}
	// A release of another button chooses nothing.
	m.Show(menuAnchor, menuLimit)
	if i, _ := m.HandlePointer(ptr(platform.Release, platform.Right, x, y)); i != -1 || !m.Open {
		t.Errorf("right release: %d, open %v", i, m.Open)
	}
	// A press elsewhere closes the menu. The release of that press is taken,
	// and no later release is.
	if i, taken := m.HandlePointer(ptr(platform.Press, platform.Left, 5, 5)); !taken || i != -1 || m.Open {
		t.Errorf("press outside: %d %v open %v", i, taken, m.Open)
	}
	if _, taken := m.HandlePointer(ptr(platform.Release, platform.Left, 5, 5)); !taken {
		t.Error("the release of the closing press was not taken")
	}
	if _, taken := m.HandlePointer(ptr(platform.Release, platform.Left, 5, 5)); taken {
		t.Error("a later release was taken")
	}
}

func TestMenuKeys(t *testing.T) {
	m := newMenu(t)
	m.Show(menuAnchor, menuLimit)
	steps := []struct {
		k     platform.Key
		hover int
	}{
		{platform.KeyDown, 2}, {platform.KeyDown, 2}, {platform.KeyUp, 1}, {platform.KeyHome, 0},
		{platform.KeyUp, 0}, {platform.KeyEnd, 2},
	}
	for _, s := range steps {
		if i := m.HandleKey(key(s.k, 0)); i != -1 || m.hover != s.hover {
			t.Errorf("key %v: chose %d, hover %d, want %d", s.k, i, m.hover, s.hover)
		}
	}
	if i := m.HandleKey(platform.KeyEvent{Key: platform.KeyEnter}); i != -1 {
		t.Errorf("a release chose %d", i)
	}
	if i := m.HandleKey(key(platform.KeyEnter, 0)); i != 2 || m.Open {
		t.Errorf("Enter chose %d, open %v", i, m.Open)
	}
	m.Show(menuAnchor, menuLimit)
	if i := m.HandleKey(platform.KeyEvent{Down: true, Rune: ' '}); i != 1 {
		t.Errorf("Space chose %d", i)
	}
	m.Show(menuAnchor, menuLimit)
	if i := m.HandleKey(key(platform.KeyEscape, 0)); i != -1 || m.Open {
		t.Errorf("Escape chose %d, open %v", i, m.Open)
	}
	if i := m.HandleKey(platform.KeyEvent{Down: true, Rune: 'x'}); i != -1 {
		t.Errorf("another key chose %d", i)
	}
}

func TestMenuDisabledItemsAndShortcuts(t *testing.T) {
	m := newMenu(t)
	plain := func() int { m.Show(menuAnchor, menuLimit); return m.Rect().Dx() }()
	m.Shortcuts = []string{"Ctrl+1"}
	m.Disabled = []bool{false, true}
	m.Show(menuAnchor, menuLimit)
	r := m.Rect()
	if r.Dx() <= plain {
		t.Errorf("width with shortcuts %d, without %d", r.Dx(), plain)
	}
	c := paint.New(menuLimit.Dx(), menuLimit.Dy())
	m.Draw(c) // draws the shortcut and the disabled item
	// A click on the disabled item chooses nothing and keeps the menu open.
	x, y := mid(menuRow(r, 1))
	m.HandlePointer(ptr(platform.Press, platform.Left, x, y))
	if i, _ := m.HandlePointer(ptr(platform.Release, platform.Left, x, y)); i != -1 || !m.Open {
		t.Errorf("click on a disabled item: %d, open %v", i, m.Open)
	}
	// Nor does Enter on it; it does on an enabled one.
	m.hover = 1
	if i := m.HandleKey(platform.KeyEvent{Key: platform.KeyEnter, Down: true}); i != -1 || !m.Open {
		t.Errorf("Enter on a disabled item: %d, open %v", i, m.Open)
	}
	m.hover = 2
	if i := m.HandleKey(platform.KeyEvent{Key: platform.KeyEnter, Down: true}); i != 2 {
		t.Errorf("Enter on item 2 chose %d", i)
	}
}
