package strictcli

// The early exit (effects contract §19.9): a command ends from anywhere in its
// call stack through the one exit step, carrying a code and the reason.

// exitNowSignal is the private value ExitNow panics with. The exit step
// recognizes it on the path the dry-run truncation signal takes (runSealed,
// invokeSealed); raised anywhere else it unwinds as any other panic does.
type exitNowSignal struct {
	code    int
	message string
}

// ExitNow ends the running command early with code, from anywhere in its call
// stack. It never returns: it panics with a private value the exit step
// recovers, so deferred functions run as the unwind passes through them, and
// then the exit step ends the command with code and records message as an
// error diagnostic -- printed as "error: <message>" in human mode, the last
// error entry of the --json document's diagnostics in machine mode.
//
// code must be between 1 and 255 and message must be non-empty; either
// violation panics at the call as a programming error. A successful run ends
// with a return from the handler.
//
// A goroutine started with a plain go statement has no frame the exit step can
// recover in: start concurrent work with Go instead.
func ExitNow(code int, message string) {
	if code < 1 || code > 255 {
		panic(errExitNowCode(code))
	}
	if message == "" {
		panic(errExitNowMessageEmpty)
	}
	panic(exitNowSignal{code: code, message: message})
}

// ExitError is the error App.Call returns when the command it invoked ended
// through ExitNow: the handler ran and ended with a failure. It is not an
// InvokeError, which means the call was refused before the handler ran.
type ExitError struct {
	Code    int
	Message string
	// Payload is what the handler supplied through ctx.Payload before its
	// early exit, nil when it supplied none (contract §19.9's box).
	Payload interface{}
}

// Error returns the handler's message alone.
func (e *ExitError) Error() string {
	return e.Message
}

// Go runs fn on a new goroutine under the dispatch's care (contract §19.9).
//
// An ExitNow or a panic raised inside fn is captured, and the handler's
// context is canceled (ctx.Done() closes) so the handler can stop waiting on
// work that will not finish. When the handler returns, the exit step waits for
// every function started through Go in this dispatch, and then ends the
// command as if the first captured value had been raised by the handler
// itself. When the handler itself ended through ExitNow or a panic, that
// ending stands and captured values are discarded after the wait.
//
// Calling Go with a Context constructed outside a command dispatch panics.
func Go(ctx *Context, fn func()) {
	if ctx == nil || ctx.effects == nil {
		panic(errEffectsUnavailable)
	}
	ctx.goroutines.Add(1)
	go func() {
		defer ctx.goroutines.Done()
		defer func() {
			if r := recover(); r != nil {
				ctx.captureGo(r)
			}
		}()
		fn()
	}()
}

// captureGo keeps the first value raised inside a function started through Go
// and cancels the handler's context.
func (c *Context) captureGo(r interface{}) {
	c.goMu.Lock()
	if !c.goHasCapture {
		c.goCaptured = r
		c.goHasCapture = true
	}
	c.goMu.Unlock()
	c.cancel()
}

// settleGoroutines waits for every function started through Go in this
// dispatch and returns the first captured value, if any.
func (c *Context) settleGoroutines() (interface{}, bool) {
	c.goroutines.Wait()
	c.goMu.Lock()
	defer c.goMu.Unlock()
	return c.goCaptured, c.goHasCapture
}

// handlerUnwind is what a handler's call left behind: nil for a return, or the
// recovered value. A value captured from a function started through Go takes
// the place of a normal return; the handler's own ending otherwise stands.
func handlerUnwind(ctx *Context, r interface{}) interface{} {
	if ctx == nil {
		return r
	}
	captured, ok := ctx.settleGoroutines()
	if r == nil && ok {
		return captured
	}
	return r
}
