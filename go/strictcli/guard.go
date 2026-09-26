package strictcli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// The runtime guard (effects contract §19.12). In machine mode, while the
// handler runs, the process stdout is redirected so that nothing reaches it
// except through the framework. The framework's own writes (the --json
// document, the document writer, an owns-stdout command's streamed child
// stdout) go to the saved real stdout; any byte that reaches the redirected
// stdout fails the run at the exit step.

// guardExcerptBytes is how many of the stray bytes the failure diagnostic
// quotes.
const guardExcerptBytes = 4096

// guardKind selects how the redirect is made.
type guardKind int

const (
	// guardFD redirects file descriptor 1 (App.Run), with os.Stdout pointing
	// at the redirected descriptor for the handler's duration.
	guardFD guardKind = iota
	// guardSwap replaces the os.Stdout variable (App.Test, whose stdout is
	// already a capture pipe installed the same way).
	guardSwap
)

// stdoutGuard is one armed redirect.
type stdoutGuard struct {
	// real is the framework's route to the real stdout while the redirect is
	// in place.
	real io.Writer

	undo func()
	r    *os.File

	mu    sync.Mutex
	total int
	head  []byte

	drained chan struct{}
}

// strayStdout is what the guard caught: the total byte count and the first
// guardExcerptBytes of the bytes.
type strayStdout struct {
	total int
	head  []byte
}

// startStdoutGuard arms the redirect. stdout is the dispatch's stdout writer
// before the redirect; for guardSwap it stays the framework's route.
func startStdoutGuard(kind guardKind, stdout io.Writer) *stdoutGuard {
	r, w, err := os.Pipe()
	if err != nil {
		panic("strictcli: the runtime guard could not create its pipe: " + err.Error())
	}
	g := &stdoutGuard{r: r, drained: make(chan struct{})}
	if kind == guardFD && fdRedirectSupported {
		realOut, undo, err := redirectStdoutFD(w)
		if err != nil {
			panic("strictcli: the runtime guard could not redirect stdout: " + err.Error())
		}
		g.real = realOut
		g.undo = undo
		// The descriptor now held as fd 1 keeps the pipe open; this copy is
		// not needed.
		w.Close()
	} else {
		saved := os.Stdout
		os.Stdout = w
		g.real = stdout
		g.undo = func() {
			os.Stdout = saved
			w.Close()
		}
	}
	go g.drain()
	return g
}

func (g *stdoutGuard) drain() {
	defer close(g.drained)
	buf := make([]byte, 32*1024)
	for {
		n, err := g.r.Read(buf)
		if n > 0 {
			g.mu.Lock()
			g.total += n
			if room := guardExcerptBytes - len(g.head); room > 0 {
				take := n
				if take > room {
					take = room
				}
				g.head = append(g.head, buf[:take]...)
			}
			g.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// guardDrainGrace bounds how long the exit step waits for the redirected
// stdout's last writer to close it. A process the handler started outside the
// effects handle may still hold the descriptor; its later bytes are not the
// run's to wait for.
const guardDrainGrace = 200 * time.Millisecond

// stop undoes the redirect and reports what was caught, or nil when nothing
// reached the redirected stdout.
func (g *stdoutGuard) stop() *strayStdout {
	g.undo()
	select {
	case <-g.drained:
	case <-time.After(guardDrainGrace):
		g.r.SetReadDeadline(time.Now())
		<-g.drained
	}
	g.r.Close()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.total == 0 {
		return nil
	}
	return &strayStdout{total: g.total, head: append([]byte(nil), g.head...)}
}

// diagnostic is the failure's error diagnostic: the total count and a JSON
// string literal of the first bytes, decoded by the replacement rule and
// encoded under the envelope's escaping regime.
func (s *strayStdout) diagnostic() string {
	text, _ := decodeUTF8Replacing(s.head, true)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(text)
	return errStdoutWrittenOutsideFramework(s.total, string(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))))
}
