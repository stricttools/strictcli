//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package strictcli

import (
	"errors"
	"os"
)

// fdRedirectSupported is false on the targets this package makes no
// operating-system redirect for (Plan 9, WebAssembly): the guard then replaces
// the os.Stdout variable instead.
const fdRedirectSupported = false

func redirectStdoutFD(w *os.File) (*os.File, func(), error) {
	return nil, nil, errors.New("file-descriptor redirect is not available on this platform")
}
