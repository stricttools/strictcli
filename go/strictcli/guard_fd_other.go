//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package strictcli

import (
	"errors"
	"os"
)

// fdRedirectSupported is false where the standard library offers no portable
// dup2: the guard then replaces the os.Stdout variable instead.
const fdRedirectSupported = false

func redirectStdoutFD(w *os.File) (*os.File, func(), error) {
	return nil, nil, errors.New("file-descriptor redirect is not available on this platform")
}
