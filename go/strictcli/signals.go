package strictcli

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Signals (effects contract §19.13). On the CLI dispatch path strictcli
// handles SIGINT and SIGTERM while the handler runs: the first signal cancels
// the handler's context and does nothing else, the handler is expected to
// notice and return, and the exit step then ends the command with 128 + the
// signal's number and an error diagnostic naming it. Handling stops as soon as
// the first signal arrives, so a second one gets the default action.

// signalWatch is the handling armed for one handler call.
type signalWatch struct {
	ch   chan os.Signal
	quit chan struct{}
	wg   sync.WaitGroup

	mu          sync.Mutex
	got         os.Signal
	handlerDone bool
}

// watchSignals arms the handling for the handler call ctx belongs to.
func watchSignals(ctx *Context) *signalWatch {
	w := &signalWatch{ch: make(chan os.Signal, 2), quit: make(chan struct{})}
	signal.Notify(w.ch, os.Interrupt, syscall.SIGTERM)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for {
			select {
			case s := <-w.ch:
				w.mu.Lock()
				if w.handlerDone {
					// The handler has already returned: outside it the signal
					// keeps its default action, which is delivered again.
					w.mu.Unlock()
					signal.Stop(w.ch)
					reraiseSignal(s)
					return
				}
				first := w.got == nil
				if first {
					w.got = s
				}
				w.mu.Unlock()
				if first {
					signal.Stop(w.ch)
					ctx.cancel()
				}
			case <-w.quit:
				return
			}
		}
	}()
	return w
}

// stop ends the handling when the handler call is over and returns the first
// signal received during it, or nil.
func (w *signalWatch) stop() os.Signal {
	w.mu.Lock()
	w.handlerDone = true
	got := w.got
	w.mu.Unlock()
	signal.Stop(w.ch)
	close(w.quit)
	w.wg.Wait()
	return got
}

// reraiseSignal delivers s to this process again, now that nothing handles it.
func reraiseSignal(s os.Signal) {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		p.Signal(s)
	}
}

// signalName is a signal's conventional name as the diagnostic spells it.
func signalName(s os.Signal) string {
	if s == os.Interrupt {
		return "SIGINT"
	}
	return "SIGTERM"
}

// signalExitStatus is 128 + the signal's number.
func signalExitStatus(s os.Signal) int {
	if n, ok := s.(syscall.Signal); ok {
		return 128 + int(n)
	}
	return 128 + int(syscall.SIGTERM)
}
