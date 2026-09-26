//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package strictcli

import (
	"os"
	"syscall"
)

const fdRedirectSupported = true

// redirectStdoutFD makes file descriptor 1 refer to w, and returns a file for
// the saved real stdout plus the function that restores it.
func redirectStdoutFD(w *os.File) (*os.File, func(), error) {
	savedFD, err := syscall.Dup(1)
	if err != nil {
		return nil, nil, err
	}
	if err := dupTo(int(w.Fd()), 1); err != nil {
		syscall.Close(savedFD)
		return nil, nil, err
	}
	realOut := os.NewFile(uintptr(savedFD), "/dev/stdout")
	undo := func() {
		dupTo(savedFD, 1)
	}
	return realOut, undo, nil
}
