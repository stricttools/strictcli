//go:build windows

package strictcli

import (
	"os"

	"golang.org/x/sys/windows"
)

const fdRedirectSupported = true

// redirectStdoutFD makes the process's standard output handle refer to w, and
// returns a file for the saved real stdout plus the function that restores it.
// It takes ownership of w, which the restore closes.
//
// os.Stdout is pointed at w as well: on Windows it holds the handle it was
// created with rather than reading the standard handle on every write, so the
// handle swap alone would not reach it. The C runtime's own descriptor table
// is not touched; a Go program without cgo never writes through it.
func redirectStdoutFD(w *os.File) (*os.File, func(), error) {
	saved, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return nil, nil, err
	}
	if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(w.Fd())); err != nil {
		return nil, nil, err
	}
	savedVar := os.Stdout
	os.Stdout = w
	realOut := os.NewFile(uintptr(saved), "/dev/stdout")
	undo := func() {
		windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, saved)
		os.Stdout = savedVar
		w.Close()
	}
	return realOut, undo, nil
}
