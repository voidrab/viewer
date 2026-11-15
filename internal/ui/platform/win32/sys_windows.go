package win32

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"viewrab/internal/ui/platform"
)

// The Win32 functions the backend calls, loaded from the system directory.
var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostMessage         = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procSetWindowText       = user32.NewProc("SetWindowTextW")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procIsZoomed            = user32.NewProc("IsZoomed")
	procIsIconic            = user32.NewProc("IsIconic")
	procSetForeground       = user32.NewProc("SetForegroundWindow")
	procAllowSetForeground  = user32.NewProc("AllowSetForegroundWindow")
	procBeginPaint          = user32.NewProc("BeginPaint")
	procEndPaint            = user32.NewProc("EndPaint")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procLoadCursor          = user32.NewProc("LoadCursorW")
	procSetCursor           = user32.NewProc("SetCursor")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetCapture          = user32.NewProc("SetCapture")
	procReleaseCapture      = user32.NewProc("ReleaseCapture")
	procTrackMouseEvent     = user32.NewProc("TrackMouseEvent")
	procScreenToClient      = user32.NewProc("ScreenToClient")
	procClientToScreen      = user32.NewProc("ClientToScreen")
	procWindowFromPoint     = user32.NewProc("WindowFromPoint")
	procGetAncestor         = user32.NewProc("GetAncestor")
	procGetKeyState         = user32.NewProc("GetKeyState")
	procMapVirtualKey       = user32.NewProc("MapVirtualKeyW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procGetSystemMenu       = user32.NewProc("GetSystemMenu")
	procEnableMenuItem      = user32.NewProc("EnableMenuItem")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procGetClipboardData    = user32.NewProc("GetClipboardData")
	procCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procSendMessage         = user32.NewProc("SendMessageW")

	procSetDIBitsToDevice = gdi32.NewProc("SetDIBitsToDevice")
	procCreateDIBSection  = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap      = gdi32.NewProc("CreateBitmap")
	procCreateCompatDC    = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC          = gdi32.NewProc("DeleteDC")
	procSelectObject      = gdi32.NewProc("SelectObject")
	procDeleteObject      = gdi32.NewProc("DeleteObject")

	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc     = kernel32.NewProc("GlobalAlloc")
	procGlobalFree      = kernel32.NewProc("GlobalFree")
	procGlobalLock      = kernel32.NewProc("GlobalLock")
	procGlobalUnlock    = kernel32.NewProc("GlobalUnlock")
	procGlobalSize      = kernel32.NewProc("GlobalSize")

	procDragAcceptFiles = shell32.NewProc("DragAcceptFiles")
	procDragQueryFile   = shell32.NewProc("DragQueryFileW")
	procDragQueryPoint  = shell32.NewProc("DragQueryPoint")
	procDragFinish      = shell32.NewProc("DragFinish")

	procGetOpenFileName  = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileName  = comdlg32.NewProc("GetSaveFileNameW")
	procCommDlgExtendErr = comdlg32.NewProc("CommDlgExtendedError")

	procDwmExtendFrame = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
)

// Window messages.
const (
	wmSize           = 0x0005
	wmSetFocus       = 0x0007
	wmKillFocus      = 0x0008
	wmPaint          = 0x000f
	wmClose          = 0x0010
	wmEraseBkgnd     = 0x0014
	wmSetCursor      = 0x0020
	wmGetMinMaxInfo  = 0x0024
	wmSetIcon        = 0x0080
	wmNCCalcSize     = 0x0083
	wmNCHitTest      = 0x0084
	wmNCActivate     = 0x0086
	wmNCLButtonDown  = 0x00a1
	wmKeyDown        = 0x0100
	wmKeyUp          = 0x0101
	wmChar           = 0x0102
	wmSysKeyDown     = 0x0104
	wmSysKeyUp       = 0x0105
	wmSysCommand     = 0x0112
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmRButtonDown    = 0x0204
	wmRButtonUp      = 0x0205
	wmMButtonDown    = 0x0207
	wmMButtonUp      = 0x0208
	wmMouseWheel     = 0x020a
	wmMouseHWheel    = 0x020e
	wmCaptureChanged = 0x0215
	wmDropFiles      = 0x0233
	wmMouseLeave     = 0x02a3
	wmApp            = 0x8000
	wmRun            = wmApp + 1 // runs the functions queued for the UI thread
)

// Window styles, extended styles and other flags.
const (
	wsOverlappedWindow = 0x00cf0000
	wsPopup            = 0x80000000
	wsExAppWindow      = 0x00040000
	wsExLayered        = 0x00080000
	wsExTransparent    = 0x00000020
	wsExToolWindow     = 0x00000080
	wsExTopmost        = 0x00000008
	wsExNoActivate     = 0x08000000

	cwUseDefault = ^uintptr(0x7fffffff) // 0x80000000 as an int

	swShowNA     = 8
	swShow       = 5
	swMaximize   = 3
	swMinimize   = 6
	swRestore    = 9
	sizeMaximize = 2
	sizeMinimize = 1

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	smCXFrame        = 32
	smCYFrame        = 33
	smCXPaddedBorder = 92

	tmeLeave = 0x00000002

	gaRoot = 2

	iconSmall = 0
	iconBig   = 1

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	dibRGBColors = 0
	biRGB        = 0

	ulwAlpha    = 0x00000002
	acSrcOver   = 0x00
	acSrcAlpha  = 0x01
	mapvkVKChar = 2

	mfByCommand = 0x0000
	mfEnabled   = 0x0000
	mfGrayed    = 0x0001

	tpmReturnCmd   = 0x0100
	tpmRightButton = 0x0002

	scSize     = 0xf000
	scMove     = 0xf010
	scMinimize = 0xf020
	scMaximize = 0xf030
	scClose    = 0xf060
	scRestore  = 0xf120

	asfwAny = ^uintptr(0) // ASFW_ANY, (DWORD)-1

	ofnAllowMultiSelect = 0x00000200
	ofnExplorer         = 0x00080000
	ofnFileMustExist    = 0x00001000
	ofnPathMustExist    = 0x00000800
	ofnOverwritePrompt  = 0x00000002
	ofnNoChangeDir      = 0x00000008
	fnerBufferTooSmall  = 0x3003
)

// hwndMessage is HWND_MESSAGE, the parent of message-only windows.
const hwndMessage = ^uintptr(2) // (HWND)-3

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

// bitmapInfo is a BITMAPINFO with no colour table, as 32-bit bitmaps have.
type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32
}

// topDown returns the BITMAPINFO of a top-down 32-bit bitmap of w by h.
func topDown(w, h int) bitmapInfo {
	return bitmapInfo{header: bitmapInfoHeader{
		size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), width: int32(w), height: -int32(h),
		planes: 1, bitCount: 32, compression: biRGB,
	}}
}

type paintStruct struct {
	hdc       uintptr
	erase     int32
	paint     rect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

type trackMouseEvent struct {
	size      uint32
	flags     uint32
	hwnd      uintptr
	hoverTime uint32
}

type minMaxInfo struct {
	reserved, maxSize, maxPosition, minTrackSize, maxTrackSize point
}

type ncCalcSizeParams struct {
	rects [3]rect
	pos   uintptr
}

type iconInfo struct {
	isIcon   int32
	xHotspot uint32
	yHotspot uint32
	mask     uintptr
	color    uintptr
}

type margins struct{ left, right, top, bottom int32 }

type size struct{ cx, cy int32 }

type blendFunction struct{ op, flags, alpha, format byte }

type openFileName struct {
	structSize    uint32
	owner         uintptr
	instance      uintptr
	filter        *uint16
	customFilter  *uint16
	maxCustFilter uint32
	filterIndex   uint32
	file          *uint16
	maxFile       uint32
	fileTitle     *uint16
	maxFileTitle  uint32
	initialDir    *uint16
	title         *uint16
	flags         uint32
	fileOffset    uint16
	fileExtension uint16
	defExt        *uint16
	custData      uintptr
	hook          uintptr
	templateName  *uint16
	reserved      uintptr
	reserved2     uint32
	flagsEx       uint32
}

// lparamPoint decodes the signed coordinates packed into a message's lParam.
func lparamPoint(lp uintptr) (x, y int32) {
	return int32(int16(lp)), int32(int16(lp >> 16))
}

// packPoint packs coordinates the way a message's lParam carries them.
func packPoint(x, y int32) uintptr {
	return uintptr(uint16(x)) | uintptr(uint16(y))<<16
}

// keyDownNow reports whether the key with virtual-key code vk is held, as the
// message being handled saw it.
func keyDownNow(vk int) bool {
	r, _, _ := procGetKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

// currentMods returns the modifiers held during the message being handled.
func currentMods() platform.Modifiers { return modifiers(keyDownNow) }

func utf16Ptr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("")
	}
	return p
}

func cursorPos() point {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}
