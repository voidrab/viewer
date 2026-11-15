package x11

import (
	"errors"
	"fmt"
)

// Core request opcodes.
const (
	opCreateWindow          = 1
	opChangeWindowAttribute = 2
	opDestroyWindow         = 4
	opMapWindow             = 8
	opConfigureWindow       = 12
	opQueryTree             = 15
	opInternAtom            = 16
	opChangeProperty        = 18
	opDeleteProperty        = 19
	opGetProperty           = 20
	opSetSelectionOwner     = 22
	opGetSelectionOwner     = 23
	opConvertSelection      = 24
	opSendEvent             = 25
	opGrabPointer           = 26
	opUngrabPointer         = 27
	opGrabKeyboard          = 31
	opUngrabKeyboard        = 32
	opQueryPointer          = 38
	opTranslateCoordinates  = 40
	opGetInputFocus         = 43
	opOpenFont              = 45
	opListFonts             = 49
	opCreatePixmap          = 53
	opFreePixmap            = 54
	opCreateGC              = 55
	opFreeGC                = 60
	opPutImage              = 72
	opCreateColormap        = 78
	opCreateGlyphCursor     = 94
	opQueryExtension        = 98
	opGetKeyboardMapping    = 101
	opGetModifierMapping    = 119
)

// Window attribute bits of CreateWindow and ChangeWindowAttributes, in the
// order their values follow.
const (
	cwBackPixmap       = 1 << 0
	cwBorderPixel      = 1 << 3
	cwBitGravity       = 1 << 4
	cwOverrideRedirect = 1 << 9
	cwEventMask        = 1 << 11
	cwColormap         = 1 << 13
	cwCursor           = 1 << 14
)

// Event mask bits.
const (
	maskKeyPress             = 1 << 0
	maskKeyRelease           = 1 << 1
	maskButtonPress          = 1 << 2
	maskButtonRelease        = 1 << 3
	maskEnterWindow          = 1 << 4
	maskLeaveWindow          = 1 << 5
	maskPointerMotion        = 1 << 6
	maskExposure             = 1 << 15
	maskStructureNotify      = 1 << 17
	maskSubstructureNotify   = 1 << 19
	maskSubstructureRedirect = 1 << 20
	maskFocusChange          = 1 << 21
	maskPropertyChange       = 1 << 22
)

// Property change modes.
const (
	propReplace = 0
	propAppend  = 2
)

// Predefined atoms.
const (
	atomNone      = 0
	atomAtom      = 4
	atomCardinal  = 6
	atomString    = 31
	atomWMHints   = 35
	atomWMName    = 39
	atomWMNormal  = 40
	atomSizeHints = 41
	atomWMClass   = 67
)

// windowAttrs encodes window attribute values, which follow their mask in
// the order of their bits.
type windowAttrs struct {
	mask   uint32
	values map[uint32]uint32
}

func (a *windowAttrs) set(bit, v uint32) *windowAttrs {
	if a.values == nil {
		a.values = map[uint32]uint32{}
	}
	a.mask |= bit
	a.values[bit] = v
	return a
}

func (a *windowAttrs) put(r *req) {
	r.u32(a.mask)
	for bit := uint32(1); bit != 0 && bit <= a.mask; bit <<= 1 {
		if a.mask&bit != 0 {
			r.u32(a.values[bit])
		}
	}
}

// changeAttrs sets attributes of window.
func changeAttrs(window uint32, a *windowAttrs) *req {
	r := newReq(opChangeWindowAttribute, 0).u32(window)
	a.put(r)
	return r
}

// changeProperty replaces or appends to a property. data holds whole units of
// format bits each, that is, format/8 bytes per unit.
func changeProperty(mode byte, window, prop, typ uint32, format byte, data []byte) *req {
	return newReq(opChangeProperty, mode).u32(window).u32(prop).u32(typ).u8(format).pad(3).
		u32(uint32(len(data) / int(format/8))).bytes(data)
}

// propU32 encodes 32-bit property values.
func propU32(vs ...uint32) []byte {
	b := make([]byte, 0, 4*len(vs))
	for _, v := range vs {
		b = order.AppendUint32(b, v)
	}
	return b
}

// clientMessage builds the 32 bytes of a ClientMessage event for window,
// of type typ, with up to five 32-bit values.
func clientMessage(window, typ uint32, data ...uint32) []byte {
	ev := make([]byte, 32)
	ev[0], ev[1] = evClientMessage, 32
	order.PutUint32(ev[4:], window)
	order.PutUint32(ev[8:], typ)
	for i, v := range data {
		order.PutUint32(ev[12+4*i:], v)
	}
	return ev
}

// sendEvent delivers ev to destination, to the clients selecting mask.
func sendEvent(destination, mask uint32, ev []byte) *req {
	return newReq(opSendEvent, 0).u32(destination).u32(mask).bytes(ev)
}

// property is the value of a window property.
type property struct {
	typ    uint32
	format byte
	value  []byte
	after  uint32 // bytes left beyond those returned
}

// getProperty reads up to max bytes of a property, deleting it after if
// del is set and it was read whole.
func (c *Conn) getProperty(window, prop uint32, del bool, max int) (property, error) {
	d := byte(0)
	if del {
		d = 1
	}
	rep, err := c.Call(newReq(opGetProperty, d).u32(window).u32(prop).u32(0).u32(0).u32(uint32(pad4(max) / 4)))
	if err != nil {
		return property{}, err
	}
	p := property{format: rep[1], typ: u32At(rep, 8), after: u32At(rep, 12)}
	n := int(u32At(rep, 16)) * int(p.format/8)
	if 32+n > len(rep) {
		return property{}, ErrShortReply
	}
	p.value = rep[32 : 32+n]
	return p, nil
}

// u32s returns a 32-bit property's values.
func (p property) u32s() []uint32 {
	if p.format != 32 {
		return nil
	}
	out := make([]uint32, len(p.value)/4)
	for i := range out {
		out[i] = u32At(p.value, 4*i)
	}
	return out
}

// internAtoms returns the atoms for names, creating them as needed. The
// requests are all sent before the first reply is awaited.
func (c *Conn) internAtoms(names []string) (map[string]uint32, error) {
	waits := make([]func() ([]byte, error), len(names))
	for i, n := range names {
		waits[i] = c.Start(newReq(opInternAtom, 0).u16(uint16(len(n))).pad(2).bytes([]byte(n)))
	}
	out := make(map[string]uint32, len(names))
	var errs []error
	for i, wait := range waits {
		rep, err := wait()
		if err != nil {
			errs = append(errs, fmt.Errorf("x11: interning %s: %w", names[i], err))
			continue
		}
		out[names[i]] = u32At(rep, 8)
	}
	return out, errors.Join(errs...)
}

// extension is a server extension's opcode and the first of its event
// codes; present is false when the server lacks it.
type extension struct {
	present    bool
	major      byte
	firstEvent byte
}

// queryExtensions asks whether the server has the extensions named, with
// every request in flight at once.
func (c *Conn) queryExtensions(names ...string) ([]extension, error) {
	waits := make([]func() ([]byte, error), len(names))
	for i, n := range names {
		waits[i] = c.Start(newReq(opQueryExtension, 0).u16(uint16(len(n))).pad(2).bytes([]byte(n)))
	}
	out := make([]extension, len(names))
	var errs []error
	for i, wait := range waits {
		rep, err := wait()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[i] = extension{present: rep[8] != 0, major: rep[9], firstEvent: rep[10]}
	}
	return out, errors.Join(errs...)
}

// translate returns where (x, y) in src is in dst, and the child of dst
// that contains it, or zero.
func (c *Conn) translate(src, dst uint32, x, y int) (dx, dy int, child uint32, err error) {
	rep, err := c.Call(newReq(opTranslateCoordinates, 0).u32(src).u32(dst).i16(x).i16(y))
	if err != nil {
		return 0, 0, 0, err
	}
	return i16At(rep, 12), i16At(rep, 14), u32At(rep, 8), nil
}
