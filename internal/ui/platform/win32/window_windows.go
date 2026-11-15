package win32

import (
	"fmt"
	"image"
	"image/draw"
	"sync"
	"time"
	"unsafe"

	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/platform/desktop"
)

// frameInterval is the shortest time between two frames, about the refresh
// rate of most screens, as GDI gives no signal of when a frame is shown.
const frameInterval = time.Second / 60

// window is a top-level window. Its title bar is the application's: the
// window keeps the frame Windows resizes, snaps and shadows, but its client
// area covers all of it.
type window struct {
	b       *Backend
	hwnd    uintptr
	events  chan platform.Event
	minSize platform.Size
	icon    uintptr

	// Touched only on the UI thread.
	cursor    uintptr // the cursor shown over the window
	inside    bool    // the pointer is in the window, and its leaving is tracked
	buttons   uint8   // the pointer buttons held, as 1<<Button
	heldLeave bool    // the pointer left while a button was held
	lastX     float64 // where the pointer was last
	lastY     float64
	chars     chars

	mu     sync.Mutex
	size   platform.Size
	state  platform.StateEvent // the state last reported
	shown  bool
	closed bool
	// Frame pacing, as in the X11 backend: busy while the last frame is
	// being shown and the interval runs, frameQueued from a FrameEvent until
	// Present answers it, and frameWanted when a frame was asked for
	// meanwhile.
	busy, frameQueued, frameWanted bool
	paced                          *time.Timer // ends busy; set on the UI thread
	// The last frame, as the BGRA rows of a top-down DIB.
	pix     []byte
	pixSize platform.Size
}

// create makes, shows and announces the window. It runs on the UI thread.
func (w *window) create(opts platform.WindowOptions) error {
	b := w.b
	b.mu.Lock()
	b.creating = w
	b.mu.Unlock()
	hwnd, _, err := procCreateWindowEx.Call(wsExAppWindow,
		uintptr(unsafe.Pointer(utf16Ptr(className))), uintptr(unsafe.Pointer(utf16Ptr(opts.Title))),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, uintptr(opts.Size.W), uintptr(opts.Size.H),
		0, 0, b.instance, 0)
	b.mu.Lock()
	b.creating = nil
	b.mu.Unlock()
	if hwnd == 0 {
		return fmt.Errorf("win32: creating a window: %w", err)
	}
	w.hwnd = hwnd
	if opts.Icon != nil {
		if w.icon = newIcon(opts.Icon); w.icon != 0 {
			procSendMessage.Call(hwnd, wmSetIcon, iconBig, w.icon)
			procSendMessage.Call(hwnd, wmSetIcon, iconSmall, w.icon)
		}
	}
	procDragAcceptFiles.Call(hwnd, 1)
	// A one-pixel top margin extended into the client area keeps the shadow
	// the desktop draws around the window.
	if procDwmExtendFrame.Find() == nil {
		m := margins{top: 1}
		procDwmExtendFrame.Call(hwnd, uintptr(unsafe.Pointer(&m)))
	}
	// The frame is worked out again, now that WM_NCCALCSIZE gives it all to
	// the client area.
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpFrameChanged|swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate)
	procShowWindow.Call(hwnd, swShow)
	w.onShown()
	return nil
}

// onShown reports the window's state and size and asks for the first frame.
func (w *window) onShown() {
	var r rect
	procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	zoomed, _, _ := procIsZoomed.Call(w.hwnd)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shown = true
	w.size = platform.Size{W: int(r.Right - r.Left), H: int(r.Bottom - r.Top)}
	w.reportState(platform.StateEvent{Maximized: zoomed != 0})
	w.send(platform.ResizeEvent{Size: w.size, Scale: 1})
	w.requestFrame()
}

// reportState reports state if it differs from the last one reported. The
// caller holds w.mu.
func (w *window) reportState(state platform.StateEvent) {
	if state != w.state && !w.closed {
		w.state = state
		w.send(state)
	}
}

// handle handles a message sent to the window. It reports false for a
// message to leave to DefWindowProc.
func (w *window) handle(message uint32, wp, lp uintptr) (uintptr, bool) {
	switch message {
	case wmNCCalcSize:
		if wp == 0 {
			return 0, false
		}
		// The client area takes the whole window. A maximised window reaches
		// past the screen by its frame, which is cut off here.
		if zoomed, _, _ := procIsZoomed.Call(w.hwnd); zoomed != 0 {
			p := (*ncCalcSizeParams)(ptr(lp))
			fx, _, _ := procGetSystemMetrics.Call(smCXFrame)
			fy, _, _ := procGetSystemMetrics.Call(smCYFrame)
			pad, _, _ := procGetSystemMetrics.Call(smCXPaddedBorder)
			dx, dy := int32(fx+pad), int32(fy+pad)
			r := &p.rects[0]
			r.Left, r.Top, r.Right, r.Bottom = r.Left+dx, r.Top+dy, r.Right-dx, r.Bottom-dy
		}
		return 0, true
	case wmNCHitTest:
		// The application finds its own edges and title bar, and asks for a
		// move or resize itself.
		return htClient, true
	case wmNCActivate:
		// Without a frame of its own to draw, the window keeps DefWindowProc
		// from painting one over it while it changes activation.
		r, _, _ := procDefWindowProc.Call(w.hwnd, uintptr(message), wp, ^uintptr(0))
		return r, true
	case wmEraseBkgnd:
		return 1, true
	case wmGetMinMaxInfo:
		info := (*minMaxInfo)(ptr(lp))
		info.minTrackSize = point{int32(w.minSize.W), int32(w.minSize.H)}
		return 0, true
	case wmSize:
		w.onSize(wp, lp)
		return 0, true
	case wmPaint:
		w.paint()
		return 0, true
	case wmClose:
		w.emit(platform.CloseEvent{})
		return 0, true
	case wmSetFocus, wmKillFocus:
		w.emit(platform.FocusEvent{Focused: message == wmSetFocus})
		return 0, true
	case wmSetCursor:
		if lp&0xffff != htClient {
			return 0, false
		}
		procSetCursor.Call(w.cursor)
		return 1, true
	case wmMouseMove, wmLButtonDown, wmLButtonUp, wmMButtonDown, wmMButtonUp, wmRButtonDown, wmRButtonUp:
		w.onButton(message, lp)
		return 0, true
	case wmMouseWheel, wmMouseHWheel:
		w.onWheel(message, wp, lp)
		return 0, true
	case wmMouseLeave:
		w.onLeave()
		return 0, true
	case wmCaptureChanged:
		w.onCaptureLost(lp)
		return 0, true
	case wmKeyDown, wmSysKeyDown:
		w.onKeyDown(wp)
		// Alt+F4 and Alt+Space are left to DefWindowProc.
		return 0, message == wmKeyDown
	case wmKeyUp, wmSysKeyUp:
		if ev, ok := keyUp(wp, currentMods()); ok && w.b.drag.from == nil {
			w.emit(ev)
		}
		// Alt or F10 on their own would open the window menu's keyboard
		// mode, which swallows the next key.
		return 0, true
	case wmChar:
		if ev, ok := w.chars.keyChar(uint16(wp), currentMods()); ok && w.b.drag.from == nil {
			w.emit(ev)
		}
		return 0, true
	case wmDropFiles:
		w.onDropFiles(wp)
		return 0, true
	}
	return 0, false
}

// onSize records a change of the window's size or state, and reports it once
// the window is shown. A minimised window keeps the size it had.
func (w *window) onSize(wp, lp uintptr) {
	if wp == sizeMinimize {
		return
	}
	size := platform.Size{W: int(lp & 0xffff), H: int(lp >> 16 & 0xffff)}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.shown {
		return // onShown reads the size the window is shown at
	}
	w.reportState(platform.StateEvent{Maximized: wp == sizeMaximize})
	if size != w.size && !w.closed {
		w.size = size
		w.send(platform.ResizeEvent{Size: size, Scale: 1})
	}
	w.requestFrame()
}

// paint answers WM_PAINT with the last frame.
func (w *window) paint() {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc != 0 {
		w.draw(hdc)
	}
	procEndPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
}

// blit shows the last frame at once.
func (w *window) blit() {
	hdc, _, _ := procGetDC.Call(w.hwnd)
	if hdc == 0 {
		return
	}
	w.draw(hdc)
	procReleaseDC.Call(w.hwnd, hdc)
}

// draw copies the last frame to hdc.
func (w *window) draw(hdc uintptr) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pix) == 0 {
		return
	}
	s := w.pixSize
	bmi := topDown(s.W, s.H)
	procSetDIBitsToDevice.Call(hdc, 0, 0, uintptr(s.W), uintptr(s.H), 0, 0, 0, uintptr(s.H),
		uintptr(unsafe.Pointer(&w.pix[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors)
}

// Events implements platform.Window.
func (w *window) Events() <-chan platform.Event { return w.events }

// emit queues ev, dropping it when the application has stalled.
func (w *window) emit(ev platform.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.send(ev)
	}
}

// send queues ev and reports whether it was queued. The caller holds w.mu
// and has checked that the window is open.
func (w *window) send(ev platform.Event) bool {
	select {
	case w.events <- ev:
		return true
	default:
		return false
	}
}

// requestFrame sends a FrameEvent now if the window may draw, and otherwise
// records that one is wanted. The caller holds w.mu.
func (w *window) requestFrame() {
	switch {
	case w.closed:
	case !w.shown || w.busy || w.frameQueued:
		w.frameWanted = true
	default:
		w.frameWanted = false
		w.frameQueued = w.send(platform.FrameEvent{})
	}
}

// Invalidate implements platform.Window.
func (w *window) Invalidate() {
	w.mu.Lock()
	w.requestFrame()
	w.mu.Unlock()
}

// Present implements platform.Window, answering the FrameEvent sent last. A
// frame that does not match the window's size is dropped, and a frame wanted
// meanwhile is then sent at once.
func (w *window) Present(img *image.RGBA) {
	size := platform.Size{W: img.Rect.Dx(), H: img.Rect.Dy()}
	w.mu.Lock()
	w.frameQueued = false
	if w.closed || !w.shown || w.busy || size != w.size || size.W == 0 || size.H == 0 {
		if w.frameWanted {
			w.requestFrame()
		}
		w.mu.Unlock()
		return
	}
	// The buffer grows only, so that a live resize does not allocate a
	// frame's memory for every frame.
	if n := 4 * size.W * size.H; cap(w.pix) < n {
		w.pix = make([]byte, n)
	} else {
		w.pix = w.pix[:n]
	}
	w.pixSize = size
	desktop.CopyBGRA(w.pix, 4*size.W, img)
	w.busy = true
	w.mu.Unlock()
	start := time.Now()
	w.b.post(func() {
		w.blit()
		delay := frameInterval - time.Since(start)
		if w.paced == nil {
			w.paced = time.AfterFunc(delay, w.frameDone)
		} else {
			w.paced.Reset(delay)
		}
	})
}

// frameDone ends the interval after a frame, sending a frame wanted
// meanwhile.
func (w *window) frameDone() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.busy = false
	if w.frameWanted {
		w.requestFrame()
	}
}

// SetTitle implements platform.Window.
func (w *window) SetTitle(title string) {
	w.b.post(func() { procSetWindowText.Call(w.hwnd, uintptr(unsafe.Pointer(utf16Ptr(title)))) })
}

// Activate implements platform.Window. Windows lets a process bring a window
// to the front only if it has been granted that right, as AllowForeground
// arranges for a launch that hands over files. The activation token is not
// used.
func (w *window) Activate(string) {
	w.b.post(func() {
		if iconic, _, _ := procIsIconic.Call(w.hwnd); iconic != 0 {
			procShowWindow.Call(w.hwnd, swRestore)
		}
		procSetForeground.Call(w.hwnd)
	})
}

// Move implements platform.Window, by handing the press to DefWindowProc as
// a press on the title bar, which moves and snaps the window.
func (w *window) Move() { w.frameDrag(htCaption) }

// Resize implements platform.Window, as Move does with a press on the frame.
func (w *window) Resize(edges platform.Edge) {
	if hit, ok := edgeHits[edges]; ok {
		w.frameDrag(hit)
	}
}

// frameDrag lets DefWindowProc carry on the press under way as a press on
// the part of the frame hit names.
func (w *window) frameDrag(hit uintptr) {
	w.b.post(func() {
		w.releasePointer()
		p := cursorPos()
		procSendMessage.Call(w.hwnd, wmNCLButtonDown, hit, packPoint(p.X, p.Y))
	})
}

// releasePointer forgets the buttons held and gives up the capture they
// took, for a move, a resize or a menu that takes the pointer from here.
// It runs on the UI thread.
func (w *window) releasePointer() {
	w.buttons = 0
	procReleaseCapture.Call()
}

// SetMaximized implements platform.Window.
func (w *window) SetMaximized(maximized bool) {
	cmd := uintptr(swRestore)
	if maximized {
		cmd = swMaximize
	}
	w.b.post(func() { procShowWindow.Call(w.hwnd, cmd) })
}

// Minimize implements platform.Window.
func (w *window) Minimize() {
	w.b.post(func() { procShowWindow.Call(w.hwnd, swMinimize) })
}

// ShowMenu implements platform.Window with the window's system menu, its
// entries enabled as the window's state allows.
func (w *window) ShowMenu(x, y int) {
	w.b.post(func() {
		menu, _, _ := procGetSystemMenu.Call(w.hwnd, 0)
		if menu == 0 {
			return
		}
		zoomed, _, _ := procIsZoomed.Call(w.hwnd)
		maximized := zoomed != 0
		for id, on := range map[uintptr]bool{
			scRestore: maximized, scMove: !maximized, scSize: !maximized,
			scMaximize: !maximized, scMinimize: true, scClose: true,
		} {
			flags := uintptr(mfByCommand | mfEnabled)
			if !on {
				flags |= mfGrayed
			}
			procEnableMenuItem.Call(menu, id, flags)
		}
		p := point{int32(x), int32(y)}
		procClientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
		w.releasePointer()
		procSetForeground.Call(w.hwnd)
		cmd, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton,
			uintptr(p.X), uintptr(p.Y), 0, w.hwnd, 0)
		if cmd != 0 {
			procPostMessage.Call(w.hwnd, wmSysCommand, cmd, 0)
		}
	})
}

// SetCursor implements platform.Window.
func (w *window) SetCursor(c platform.Cursor) {
	w.b.post(func() {
		w.cursor = w.b.loadCursor(c)
		if w.inside {
			procSetCursor.Call(w.cursor)
		}
	})
}

// loadCursor returns the system cursor that shows c. It runs on the UI
// thread; LoadCursor returns the same shared handle each time.
func (b *Backend) loadCursor(c platform.Cursor) uintptr {
	h, _, _ := procLoadCursor.Call(0, cursorID(c))
	if h == 0 {
		return b.arrow
	}
	return h
}

// Close implements platform.Window: it destroys the window and closes the
// Events channel.
func (w *window) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	close(w.events)
	w.mu.Unlock()
	w.b.post(func() {
		if g := &w.b.drag; g.from != nil && (g.from == w || g.over == w) {
			g.cancel()
		}
		w.b.mu.Lock()
		delete(w.b.windows, w.hwnd)
		w.b.mu.Unlock()
		procDestroyWindow.Call(w.hwnd)
		if w.icon != 0 {
			procDestroyIcon.Call(w.icon)
		}
	})
}

// onButton handles pointer motion and button presses and releases. The first
// press captures the pointer, so that the window receives its motion and
// releases until the last button is up, even outside the window.
func (w *window) onButton(message uint32, lp uintptr) {
	px, py := lparamPoint(lp)
	if g := &w.b.drag; g.from != nil {
		p := point{px, py}
		procClientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
		switch message {
		case wmMouseMove:
			g.update(p)
		case wmLButtonUp, wmMButtonUp, wmRButtonUp:
			g.drop(p)
		}
		return
	}
	x, y := float64(px), float64(py)
	mods := currentMods()
	if !w.inside {
		w.inside = true
		tme := trackMouseEvent{flags: tmeLeave, hwnd: w.hwnd}
		tme.size = uint32(unsafe.Sizeof(tme))
		procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		w.emit(platform.PointerEvent{Kind: platform.Enter, X: x, Y: y, Mods: mods})
	}
	w.lastX, w.lastY = x, y
	var button platform.Button
	switch message {
	case wmMouseMove:
		w.emit(platform.PointerEvent{Kind: platform.Move, X: x, Y: y, Mods: mods})
		return
	case wmLButtonDown, wmLButtonUp:
		button = platform.Left
	case wmMButtonDown, wmMButtonUp:
		button = platform.Middle
	default:
		button = platform.Right
	}
	bit := uint8(1) << button
	switch message {
	case wmLButtonDown, wmMButtonDown, wmRButtonDown:
		if w.buttons == 0 {
			procSetCapture.Call(w.hwnd)
		}
		w.buttons |= bit
		w.emit(platform.PointerEvent{Kind: platform.Press, Button: button, X: x, Y: y, Mods: mods})
	default:
		if w.buttons&bit == 0 {
			return
		}
		w.buttons &^= bit
		w.emit(platform.PointerEvent{Kind: platform.Release, Button: button, X: x, Y: y, Mods: mods})
		if w.buttons == 0 {
			procReleaseCapture.Call()
			if w.heldLeave || !w.contains(px, py) {
				w.leave()
			}
		}
	}
}

// contains reports whether the point (x, y) is in the client area.
func (w *window) contains(x, y int32) bool {
	var r rect
	procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

// onWheel reports the turn of a wheel, which Windows gives in screen
// coordinates.
func (w *window) onWheel(message uint32, wp, lp uintptr) {
	if w.b.drag.from != nil {
		return
	}
	sx, sy := lparamPoint(lp)
	p := point{sx, sy}
	procScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	dx, dy := wheel(int16(wp>>16), message == wmMouseHWheel)
	w.emit(platform.ScrollEvent{DX: dx, DY: dy, X: float64(p.X), Y: float64(p.Y), Mods: currentMods()})
}

// onLeave handles the pointer leaving the window, which counts only once no
// button holds it there.
func (w *window) onLeave() {
	if !w.inside {
		return
	}
	if w.buttons != 0 {
		w.heldLeave = true
		return
	}
	w.leave()
}

// leave reports the pointer gone.
func (w *window) leave() {
	w.inside, w.heldLeave = false, false
	w.emit(platform.PointerEvent{Kind: platform.Leave, X: w.lastX, Y: w.lastY, Mods: currentMods()})
}

// onCaptureLost handles the window losing the pointer to another, newcap,
// or to none: a drag from it is cancelled, and the buttons count as up.
func (w *window) onCaptureLost(newcap uintptr) {
	if newcap == w.hwnd {
		return
	}
	if g := &w.b.drag; g.from == w {
		g.cancel()
	}
	w.buttons = 0
	if w.heldLeave {
		w.leave()
	}
}

// onKeyDown handles a key press. Escape cancels a drag under way, and other
// keys do nothing meanwhile.
func (w *window) onKeyDown(vk uintptr) {
	if g := &w.b.drag; g.from != nil {
		if vk == vkEscape {
			g.cancel()
		}
		return
	}
	char, _, _ := procMapVirtualKey.Call(vk, mapvkVKChar)
	if ev, ok := keyDown(vk, char, currentMods()); ok {
		w.emit(ev)
	}
}

// onDropFiles reports files dropped from another program.
func (w *window) onDropFiles(drop uintptr) {
	defer procDragFinish.Call(drop)
	n, _, _ := procDragQueryFile.Call(drop, 0xffffffff, 0, 0)
	var paths []string
	for i := range n {
		l, _, _ := procDragQueryFile.Call(drop, i, 0, 0)
		buf := make([]uint16, l+1)
		procDragQueryFile.Call(drop, i, uintptr(unsafe.Pointer(&buf[0])), l+1)
		paths = append(paths, chosenFirst(buf))
	}
	var p point
	procDragQueryPoint.Call(drop, uintptr(unsafe.Pointer(&p)))
	w.emit(platform.DropEvent{X: float64(p.X), Y: float64(p.Y), Paths: paths})
}

// SetClipboard implements platform.Window.
func (w *window) SetClipboard(text string) {
	units := clipboardText(text)
	w.b.post(func() {
		if !openClipboard(w.hwnd) {
			return
		}
		defer procCloseClipboard.Call()
		procEmptyClipboard.Call()
		h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(2*len(units)))
		if h == 0 {
			return
		}
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			procGlobalFree.Call(h)
			return
		}
		copy(unsafe.Slice((*uint16)(ptr(p)), len(units)), units)
		procGlobalUnlock.Call(h)
		if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
			procGlobalFree.Call(h)
		}
	})
}

// RequestPaste implements platform.Window.
func (w *window) RequestPaste() {
	w.b.post(func() {
		if !openClipboard(w.hwnd) {
			return
		}
		defer procCloseClipboard.Call()
		h, _, _ := procGetClipboardData.Call(cfUnicodeText)
		if h == 0 {
			return
		}
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			return
		}
		n, _, _ := procGlobalSize.Call(h)
		units := unsafe.Slice((*uint16)(ptr(p)), min(n/2, desktop.MaxPaste/2))
		text := pastedText(units)
		procGlobalUnlock.Call(h)
		w.emit(platform.PasteEvent{Text: text})
	})
}

// openClipboard opens the clipboard for hwnd, retrying for a moment while
// another program has it open.
func openClipboard(hwnd uintptr) bool {
	for range 10 {
		if r, _, _ := procOpenClipboard.Call(hwnd); r != 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// newIcon makes an icon of img, or returns zero if it cannot.
func newIcon(img image.Image) uintptr {
	b := img.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(nrgba, nrgba.Rect, img, b.Min, draw.Src)
	// The colour of an icon is not premultiplied, so the NRGBA pixels are
	// passed to CopyBGRA as if they were premultiplied RGBA. CopyBGRA then
	// only swaps the red and blue bytes.
	color := newDIB(&image.RGBA{Pix: nrgba.Pix, Stride: nrgba.Stride, Rect: nrgba.Rect})
	if color == 0 {
		return 0
	}
	defer procDeleteObject.Call(color)
	mask, _, _ := procCreateBitmap.Call(uintptr(b.Dx()), uintptr(b.Dy()), 1, 1, 0)
	if mask == 0 {
		return 0
	}
	defer procDeleteObject.Call(mask)
	ii := iconInfo{isIcon: 1, mask: mask, color: color}
	icon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	return icon
}

// newDIB returns a 32-bit DIB section holding img, or zero if it cannot be
// made.
func newDIB(img *image.RGBA) uintptr {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 {
		return 0
	}
	bmi := topDown(w, h)
	var bits uintptr
	bmp, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bmi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == 0 {
		return 0
	}
	desktop.CopyBGRA(unsafe.Slice((*byte)(ptr(bits)), 4*w*h), 4*w, img)
	return bmp
}

// ptr turns an address Windows returned into a pointer. The memory it points
// to is not Go's, so the garbage collector need not know of it.
func ptr(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}
