//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package strictcli

import (
	"os"

	"golang.org/x/sys/unix"
)

const fdRedirectSupported = true

// redirectStdoutFD makes file descriptor 1 refer to w, and returns a file for
// the saved real stdout plus the function that restores it. It takes
// ownership of w: once descriptor 1 holds the pipe, w itself is closed.
//
// golang.org/x/sys/unix offers dup2 on every Unix Go supports, including
// Solaris, illumos and AIX, where the standard syscall package does not.
func redirectStdoutFD(w *os.File) (*os.File, func(), error) {
	savedFD, err := unix.Dup(1)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.Dup2(int(w.Fd()), 1); err != nil {
		unix.Close(savedFD)
		return nil, nil, err
	}
	w.Close()
	realOut := os.NewFile(uintptr(savedFD), "/dev/stdout")
	undo := func() {
		unix.Dup2(savedFD, 1)
	}
	return realOut, undo, nil
}
