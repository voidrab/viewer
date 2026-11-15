package win32

import (
	"errors"
	"fmt"
	"runtime"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// openBufferSize is how many code units the open dialog may write: enough
// for many files chosen at once. saveBufferSize is a path's limit.
const (
	openBufferSize = 1 << 16
	saveBufferSize = windows.MAX_LONG_PATH
)

// PickPDFs shows the common Open dialog, titled title, and returns the files
// chosen, or none if the user cancelled.
func PickPDFs(title string) ([]string, error) {
	buf := dialogBuffer(openBufferSize, "")
	ok, err := fileDialog(procGetOpenFileName, title, buf,
		ofnExplorer|ofnAllowMultiSelect|ofnFileMustExist|ofnPathMustExist|ofnNoChangeDir)
	if !ok {
		return nil, err
	}
	return chosenFiles(buf), nil
}

// SavePDF shows the common Save dialog, titled title and suggesting the name
// suggested, and returns the path chosen, or "" if the user cancelled.
func SavePDF(title, suggested string) (string, error) {
	buf := dialogBuffer(saveBufferSize, suggested)
	ok, err := fileDialog(procGetSaveFileName, title, buf,
		ofnExplorer|ofnOverwritePrompt|ofnPathMustExist|ofnNoChangeDir)
	if !ok {
		return "", err
	}
	return chosenFirst(buf), nil
}

// fileDialog runs the common file dialog proc with the PDF filter, its result
// written into buf. It reports false, with no error, when the user cancelled.
func fileDialog(proc *windows.LazyProc, title string, buf []uint16, flags uint32) (bool, error) {
	// The dialog runs a modal loop for windows of its own on this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	filter := utf16.Encode([]rune(pdfFilter))
	ofn := openFileName{
		filter:  &filter[0],
		file:    &buf[0],
		maxFile: uint32(len(buf)),
		title:   utf16Ptr(title),
		flags:   flags,
		defExt:  utf16Ptr("pdf"),
	}
	ofn.structSize = uint32(unsafe.Sizeof(ofn))
	if r, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn))); r != 0 {
		return true, nil
	}
	code, _, _ := procCommDlgExtendErr.Call()
	switch code {
	case 0:
		return false, nil
	case fnerBufferTooSmall:
		return false, errors.New("too many files chosen at once")
	}
	return false, fmt.Errorf("the file dialog failed (error %#x)", code)
}

// The registry key and value that say whether programs use the light theme.
const (
	personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	lightValue     = "AppsUseLightTheme"
)

// PrefersDark reports whether the user chose the dark mode for programs, and
// whether the choice could be read.
func PrefersDark() (dark, ok bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE)
	if err != nil {
		return false, false
	}
	defer k.Close()
	return readDark(k)
}

func readDark(k registry.Key) (dark, ok bool) {
	v, _, err := k.GetIntegerValue(lightValue)
	if err != nil {
		return false, false
	}
	return v == 0, true
}

// WatchDarkMode calls changed with the user's choice of light or dark mode
// each time it changes, for as long as the program runs. If the registry key
// cannot be opened, it does nothing.
func WatchDarkMode(changed func(dark bool)) {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE|registry.NOTIFY)
	if err != nil {
		return
	}
	go func() {
		defer k.Close()
		last, _ := readDark(k)
		for {
			if windows.RegNotifyChangeKeyValue(windows.Handle(k), false,
				windows.REG_NOTIFY_CHANGE_LAST_SET, 0, false) != nil {
				return
			}
			if dark, ok := readDark(k); ok && dark != last {
				last = dark
				changed(dark)
			}
		}
	}()
}

// Open opens uri in the program the user picked for its kind of address.
func Open(uri string) error {
	return windows.ShellExecute(0, utf16Ptr("open"), utf16Ptr(uri), nil, nil, swShow)
}

// AllowForeground lets the running instance of the program bring its window
// to the front for the launch handing it files: Windows otherwise lets only
// the program the user last acted on do that.
func AllowForeground() {
	procAllowSetForeground.Call(asfwAny)
}
