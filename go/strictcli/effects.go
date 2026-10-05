package strictcli

// The effects regime.
//
// Command classification (read_only / mutating), the ctx.Effects() handle, dry
// mode's would-do log, and the carriers that make a data-flow preview complete
// without letting the framework invent a value it cannot know.
//
// Two rules govern the whole regime: FAIL CLOSED (when the framework cannot
// prove an operation is safe to preview, it stops with a precise error instead
// of guessing) and ZERO INFERENCE (nothing is inferred -- not classification,
// not whether an argument is a path, not whether a resource is current).
//
// Go returns a CARRIER TYPE ALWAYS (§2.5.3). Completed, Spawned and Response are
// settleable carriers: their extractors return real values in live mode and
// panic with the truncation error when unsettled. Unsettled is the payload-less
// VOID carrier returned by the five path-mutating methods in both modes -- it
// never carries a value, is never forwardable, and its extractors panic always.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

// --- classification -------------------------------------------------------

// The two legal command classifications. There is no default: every command
// declares one through WithEffect, and a command registered without it is a
// registration-time hard error. Deprecated commands are exempt (no handler).
const (
	EffectReadOnly = "read_only"
	EffectMutating = "mutating"
)

// Effect kinds. CacheWrite has NO public method: it is minted only by
// framework-internal code (the test-coverage shards and manifest) and is
// unreachable from application code.
const (
	ProcMutate = "proc_mutate"
	ProcSpawn  = "proc_spawn"
	FileWrite  = "file_write"
	NetMutate  = "net_mutate"
	CacheWrite = "cache_write"
)

// grantableKinds are the kinds a Grant may be declared for. CacheWrite is
// excluded: it is unreachable from application code, so nothing could ever use
// such a grant.
var grantableKinds = []string{ProcMutate, ProcSpawn, FileWrite, NetMutate}

// Grant is a per-command, per-effect-kind authorization with a mandatory reason.
//
// A grant is not permission to do something otherwise forbidden; it is a
// labelled reason that surfaces in the preview so a reviewer reading a dry run
// sees why a dangerous step is there.
type Grant struct {
	Name   string
	Reason string
	Kind   string
}

// Forwarding declares that a handler deliberately accepts and forwards the
// app's global flag values. In Go the declaration is inert beyond the schema
// emission (guard v2's enforcement is Python-only, §10.3); it exists so the API
// surface stays in parity and consumers can label forwarding wrappers uniformly.
type Forwarding struct {
	Reason string
}

// frameworkInternalForwardingReason is the one reason string strictcli's own
// auto-registered commands use. Their handlers absorb the app's app-defined
// global flag values, which a framework-authored handler cannot name.
const frameworkInternalForwardingReason = "framework-internal: absorbs app-defined global flag values"

// --- the truncation panic -------------------------------------------------

// dryRunTruncation is the panic value raised by a carrier's extractor. It is a
// distinct unexported type so the dispatch sites can recover exactly this and
// re-panic anything else.
type dryRunTruncation struct {
	message string
	log     *effectLog
	// The three values §12.5's text is built from, kept apart from it so the
	// envelope's preview_error can carry them as members (§19.3) without
	// re-parsing the rendered message.
	step    int
	cmdPath string
	brand   string
}

// --- carriers -------------------------------------------------------------

// Unsettled is Go's VOID carrier: the value returned by write, mkdir, remove,
// rename and chmod in BOTH modes. It never carries a value, only a brand, and
// exists solely to give every Go effect method one uniform return shape. Its
// extractors panic in both modes, and it is never forwardable -- passing one
// into a later effect is a call-time hard error.
//
// The [0]func() field makes the struct non-comparable: `u == v` is a compile
// error. That is the only compile-time protection Go can offer (§17).
type Unsettled struct {
	_       [0]func()
	brand   string
	log     *effectLog
	cmdPath string
}

func (u Unsettled) brandForm() string { return u.brand }

func (u Unsettled) truncate() dryRunTruncation {
	return truncationFor(u.log, u.cmdPath, u.brand)
}

// String panics: stringifying a carrier is extraction, not forwarding.
func (u Unsettled) String() string { panic(u.truncate()) }

// Bytes panics: extraction.
func (u Unsettled) Bytes() []byte { panic(u.truncate()) }

// Int panics: extraction.
func (u Unsettled) Int() int64 { panic(u.truncate()) }

// Bool panics: extraction (and branching).
func (u Unsettled) Bool() bool { panic(u.truncate()) }

// Completed is the result of a subprocess that ran to completion, and a
// settleable carrier. Stdout/Stderr are the child's output decoded as UTF-8
// strictly with a single trailing newline removed -- the form that can be
// forwarded straight into a later effect's argv.
type Completed struct {
	_        [0]func()
	settled  bool
	exitCode int
	stdout   string
	stderr   string
	brand    string
	log      *effectLog
	cmdPath  string
}

func (c Completed) brandForm() string { return c.brand }

func (c Completed) truncate() dryRunTruncation {
	return truncationFor(c.log, c.cmdPath, c.brand)
}

// ExitCode returns the child's exit status. Panics when unsettled.
func (c Completed) ExitCode() int {
	if !c.settled {
		panic(c.truncate())
	}
	return c.exitCode
}

// Stdout returns the child's captured stdout. Panics when unsettled.
func (c Completed) Stdout() string {
	if !c.settled {
		panic(c.truncate())
	}
	return c.stdout
}

// Stderr returns the child's captured stderr. Panics when unsettled.
func (c Completed) Stderr() string {
	if !c.settled {
		panic(c.truncate())
	}
	return c.stderr
}

// Spawned is a handle for a started-but-not-awaited child process, and a
// settleable carrier. It has NO scalar projection: forwarding a Spawned into a
// string position is a call-time hard error.
//
// Every child a handler starts is the handler's to finish: Wait on it, or Kill
// it. One still running when the handler ends is killed by the exit step and
// fails the run (contract §19.11's box).
type Spawned struct {
	_       [0]func()
	settled bool
	pid     int
	argv    string
	brand   string
	log     *effectLog
	cmdPath string
	// child is the dispatch's record of the process, shared by every copy of
	// this handle and by the exit step that settles it.
	child *spawnedChild
}

func (s Spawned) brandForm() string { return s.brand }

func (s Spawned) truncate() dryRunTruncation {
	return truncationFor(s.log, s.cmdPath, s.brand)
}

// PID returns the child's process id. Panics when unsettled.
func (s Spawned) PID() int {
	if !s.settled {
		panic(s.truncate())
	}
	return s.pid
}

// Wait waits for the child and returns its Completed result. It honors
// Check(bool), Timeout(d), and Redact(values...) and nothing else: with the
// default true a nonzero exit is an error, mirroring run's opt-out; with a
// Timeout the child is killed once d has passed and Wait returns an error
// naming it; Redact applies to the errors Wait returns. Calling Wait on an
// unsettled Spawned is extraction and truncates.
func (s Spawned) Wait(opts ...EffectOption) (_ Completed, err error) {
	if !s.settled {
		panic(s.truncate())
	}
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(s.cmdPath, "spawn", opts, acceptedWait)
	if err != nil {
		return Completed{}, err
	}
	// A captured child's stdout is drained no later than its Wait (§19.11).
	s.child.markWaited()
	if o.timeout > 0 {
		timer := time.NewTimer(o.timeout)
		select {
		case <-s.child.exited:
			timer.Stop()
		case <-timer.C:
			if !s.child.hasExited() {
				killErr := s.child.signal(os.Kill)
				s.child.reap()
				if killErr != nil {
					return Completed{}, killErr
				}
				return Completed{}, errors.New(errEffectTimedOut(s.cmdPath, "spawn", s.argv, o.timeout.String()))
			}
		}
	}
	s.child.reap()
	waitErr := s.child.waitErr
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return Completed{}, waitErr
	}
	code := s.child.cmd.ProcessState.ExitCode()
	if o.check && code != 0 {
		return Completed{}, errors.New(errEffectRunFailed(s.cmdPath, "spawn", s.argv, code))
	}
	// spawn always streams (the child inherits stdio), so there is nothing
	// captured to report.
	return Completed{settled: true, exitCode: code}, nil
}

// Signal sends sig to the child and returns at once; the child is still the
// handler's to Wait on or Kill. A child that has already exited is left alone,
// because its process id may already name another process. Calling Signal on
// an unsettled Spawned is extraction and truncates.
func (s Spawned) Signal(sig os.Signal) error {
	if !s.settled {
		panic(s.truncate())
	}
	return s.child.signal(sig)
}

// Kill kills the child (SIGKILL, or the platform's forced termination) and
// waits for it to exit. A killed child counts as waited on: the exit step does
// not settle it again, and a later Wait reports its status. A child that has
// already exited is left alone. Calling Kill on an unsettled Spawned is
// extraction and truncates.
func (s Spawned) Kill() error {
	if !s.settled {
		panic(s.truncate())
	}
	s.child.markWaited()
	err := s.child.signal(os.Kill)
	s.child.reap()
	return err
}

// spawnedChild is one child started through Spawn in a dispatch (contract
// §19.11's box). Its process is reaped by a goroutine of its own, so whether
// it has exited is known without waiting for it.
type spawnedChild struct {
	cmd  *exec.Cmd
	argv string
	// exited is closed once cmd.Wait has returned, with its error in waitErr.
	exited  chan struct{}
	waitErr error
	// drained is closed when the child's captured stdout has been read to its
	// end (§19.11); nil when the child's stdout is not captured.
	drained chan struct{}

	mu     sync.Mutex
	waited bool
}

// childTermGrace is how long a child the exit step sent SIGTERM gets before
// SIGKILL.
const childTermGrace = time.Second

// timeoutWaitDelay is how long a run killed by its Timeout waits for its
// output pipes to close (a grandchild may hold them) before Run returns.
const timeoutWaitDelay = time.Second

func startChildReaper(cmd *exec.Cmd, argv string, drained chan struct{}) *spawnedChild {
	c := &spawnedChild{cmd: cmd, argv: argv, exited: make(chan struct{}), drained: drained}
	go func() {
		c.waitErr = cmd.Wait()
		close(c.exited)
	}()
	return c
}

func (c *spawnedChild) markWaited() {
	c.mu.Lock()
	c.waited = true
	c.mu.Unlock()
}

// claimForSettling reports whether the handler left this child unwaited, and
// marks it waited either way.
func (c *spawnedChild) claimForSettling() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.waited {
		return false
	}
	c.waited = true
	return true
}

func (c *spawnedChild) hasExited() bool {
	select {
	case <-c.exited:
		return true
	default:
		return false
	}
}

// signal delivers sig unless the child has already exited.
func (c *spawnedChild) signal(sig os.Signal) error {
	if c.hasExited() {
		return nil
	}
	if err := c.cmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// reap waits for the child to exit and its captured stdout to drain.
func (c *spawnedChild) reap() {
	<-c.exited
	if c.drained != nil {
		<-c.drained
	}
}

// settle reaps an unwaited child, killing it first when it still runs: SIGTERM,
// then SIGKILL after childTermGrace. Where SIGTERM cannot be sent (Windows) the
// child is killed at once. It returns the diagnostic naming a child that had
// to be killed, or "".
func (c *spawnedChild) settle() string {
	if !c.claimForSettling() {
		return ""
	}
	if c.hasExited() {
		c.reap()
		return ""
	}
	if err := c.signal(syscall.SIGTERM); err != nil {
		c.signal(os.Kill)
	}
	select {
	case <-c.exited:
	case <-time.After(childTermGrace):
		c.signal(os.Kill)
	}
	c.reap()
	return errChildKilledAtExit(c.cmd.Process.Pid, c.argv)
}

// Response is the result of an HTTP request, and a settleable carrier. Header
// names are lower-cased.
type Response struct {
	_       [0]func()
	settled bool
	status  int
	body    []byte
	headers map[string]string
	brand   string
	log     *effectLog
	cmdPath string
}

func (r Response) brandForm() string { return r.brand }

func (r Response) truncate() dryRunTruncation {
	return truncationFor(r.log, r.cmdPath, r.brand)
}

// Status returns the HTTP status code. Panics when unsettled.
func (r Response) Status() int {
	if !r.settled {
		panic(r.truncate())
	}
	return r.status
}

// Body returns the raw response body. Panics when unsettled.
func (r Response) Body() []byte {
	if !r.settled {
		panic(r.truncate())
	}
	return r.body
}

// Header returns a response header by (case-insensitive) name. Panics when
// unsettled.
func (r Response) Header(name string) string {
	if !r.settled {
		panic(r.truncate())
	}
	return r.headers[strings.ToLower(name)]
}

func truncationFor(log *effectLog, cmdPath, brand string) dryRunTruncation {
	step := 1
	if log != nil {
		step = log.nextSeq()
	}
	return dryRunTruncation{
		message: errDryRunTruncated(step, cmdPath, brand),
		log:     log,
		step:    step,
		cmdPath: cmdPath,
		brand:   brand,
	}
}

// --- effect options -------------------------------------------------------

// EffectOption is one trailing option on an effects-handle call. Its canonical
// snake_case name is what errEffectOptionNotAccepted renders, so the message is
// byte-identical across the three implementations even though the constructor
// is spelled Stream(bool).
type EffectOption struct {
	name  string
	value interface{}
	key   string // second component (header name), unused otherwise
}

// Resource declares an opaque resource token naming what the effect produces.
// Declared metadata only: it never gates, skips, orders or deduplicates
// anything.
func Resource(token string) EffectOption {
	return EffectOption{name: "resource", value: token}
}

// SkipIfCurrent declares a preview-only conditional annotation. In dry mode it
// renders a suffix on the log line; in real mode the effect executes
// unconditionally. There is no currency machinery of any kind behind it.
func SkipIfCurrent(token string) EffectOption {
	return EffectOption{name: "skip_if_current", value: token}
}

// UseGrant names a grant declared on the running command. Its kind must match
// the effect's kind.
func UseGrant(name string) EffectOption {
	return EffectOption{name: "grant", value: name}
}

// Cwd sets the working directory of a run or spawn.
func Cwd(dir string) EffectOption {
	return EffectOption{name: "cwd", value: dir}
}

// EffectEnv merges environment entries OVER the inherited environment, never
// replacing it.
//
// Named EffectEnv rather than Env because the package already exports
// Env(varName string) FlagOption and Go has no overloading.
func EffectEnv(env map[string]string) EffectOption {
	return EffectOption{name: "env", value: env}
}

// Check opts a single call out of the "a failed operation is an error" rule:
// with Check(false) the result is returned with its real exit code / status and
// the handler decides.
func Check(check bool) EffectOption {
	return EffectOption{name: "check", value: check}
}

// Stream makes a run inherit stdout/stderr instead of capturing them; the
// returned Stdout/Stderr are then empty strings.
func Stream(stream bool) EffectOption {
	return EffectOption{name: "stream", value: stream}
}

// Body sets an HTTP request body. A body is a payload, not a name: it is not a
// carrier-accepting position.
func Body(body []byte) EffectOption {
	return EffectOption{name: "body", value: body}
}

// Header adds one HTTP request header. Repeat it for several headers.
func Header(name, value string) EffectOption {
	return EffectOption{name: "headers", key: name, value: value}
}

// Stdin writes data to a run or spawn child's stdin, then closes it. It is the
// way to hand a child a secret: argv is visible to every process on the
// machine, stdin is not. The content is never echoed anywhere the framework
// renders an effect: the would-do log and the effect record show only its
// byte count, and no error or verbose output contains it.
func Stdin(data []byte) EffectOption {
	return EffectOption{name: "stdin", value: data}
}

// Timeout bounds a run, a Spawned's Wait, or one HTTP request. On a run or a
// Wait, once d has passed the child is killed and the call returns an error
// naming the command and the timeout; on HTTP, the request is abandoned and the
// call returns an error naming the request and the timeout. A non-positive d is
// a call-time hard error.
func Timeout(d time.Duration) EffectOption {
	return EffectOption{name: "timeout", value: d}
}

// Read declares that an HTTP request changes nothing on the server. A declared
// read is legal in a read_only command, is never written to the effect log, and
// is performed for real under --dry-run, the same as an observe. After a
// mutation has been recorded in a dry run it is not performed and returns a
// stale brand instead, because what it would read depends on effects that did
// not happen. It never carries a grant. The framework never infers a read from
// the HTTP method: without Read, every request is a recorded mutation.
func Read() EffectOption {
	return EffectOption{name: "read", value: true}
}

// Observe declares that a run changes nothing. It means what an argv matching
// the app's proc_observe_allowlist means: legal in a read_only command, never
// written to the effect log, performed for real under --dry-run (a stale brand
// once a mutation has been recorded), and never carrying a grant.
func Observe() EffectOption {
	return EffectOption{name: "observe", value: true}
}

// Redact replaces every occurrence of each value with «redacted» wherever the
// framework renders this effect: the detail (argv, path, or URL), the resource
// and skip_if_current tokens, the would-do log, the effect record, a stale
// brand, and every error the call returns. An empty value, or no value at all,
// is a call-time hard error. A redacted error still answers errors.Is for the
// error it replaced, but does not unwrap to it, since that one carries the
// value.
func Redact(values ...string) EffectOption {
	return EffectOption{name: "redact", value: append([]string(nil), values...)}
}

// Mode sets the permission bits of the file a Write produces. The file has
// that mode once Write returns, whether Write created it or it already
// existed, and the mode is set before any content is written. A mode with any
// bit outside os.ModePerm is a call-time hard error. Dry mode renders it on
// the log line (mode: 0600) and in the record (mode). Without Mode, a file
// Write creates gets 0644 and an existing file keeps its mode.
func Mode(mode os.FileMode) EffectOption {
	return EffectOption{name: "mode", value: mode}
}

// The accepted option set per method (§2.5.2). Every method also accepts the
// three common options of §2.3.
var (
	acceptedRun   = optionSet("cwd", "env", "check", "stream", "stdin", "timeout", "observe", "redact", "resource", "skip_if_current", "grant")
	acceptedSpawn = optionSet("cwd", "env", "stdin", "redact", "resource", "skip_if_current", "grant")
	acceptedPath  = optionSet("redact", "resource", "skip_if_current", "grant")
	acceptedWrite = optionSet("mode", "redact", "resource", "skip_if_current", "grant")
	acceptedHTTP  = optionSet("body", "headers", "check", "timeout", "read", "redact", "resource", "skip_if_current", "grant")
	acceptedWait  = optionSet("check", "timeout", "redact")
)

func optionSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// effectOpts is the resolved option state of one call.
type effectOpts struct {
	cwd           string
	env           map[string]string
	check         bool
	stream        bool
	resource      string
	skipIfCurrent string
	grant         string
	body          []byte
	headers       map[string]string
	stdin         []byte
	timeout       time.Duration
	mode          os.FileMode
	read          bool
	observe       bool
	// redactor replaces every Redact value with redactedMarker; nil when the
	// call declares none.
	redactor *strings.Replacer

	hasStdin         bool
	hasBody          bool
	hasMode          bool
	hasResource      bool
	hasSkipIfCurrent bool
	hasGrant         bool
}

// parseEffectOptions validates the variadic list against the receiving method's
// accepted set and resolves it. An option a method does not accept is a
// call-time hard error -- silently ignoring one is the single outcome
// declare-everything cannot have.
func parseEffectOptions(cmdPath, method string, opts []EffectOption, accepted map[string]bool) (effectOpts, error) {
	resolved := effectOpts{check: true}
	var redact []string
	for _, o := range opts {
		name := o.name
		if !accepted[name] {
			return resolved, errors.New(errEffectOptionNotAccepted(cmdPath, method, name))
		}
		switch name {
		case "cwd":
			resolved.cwd = o.value.(string)
		case "env":
			resolved.env = o.value.(map[string]string)
		case "check":
			resolved.check = o.value.(bool)
		case "stream":
			resolved.stream = o.value.(bool)
		case "resource":
			resolved.resource = o.value.(string)
			resolved.hasResource = true
		case "skip_if_current":
			resolved.skipIfCurrent = o.value.(string)
			resolved.hasSkipIfCurrent = true
		case "grant":
			resolved.grant = o.value.(string)
			resolved.hasGrant = true
		case "body":
			resolved.body = o.value.([]byte)
			resolved.hasBody = true
		case "read":
			resolved.read = true
		case "observe":
			resolved.observe = true
		case "redact":
			values := o.value.([]string)
			if len(values) == 0 {
				return resolved, errors.New(errEffectRedactNoValues(cmdPath, method))
			}
			for _, v := range values {
				if v == "" {
					return resolved, errors.New(errEffectRedactEmptyValue(cmdPath, method))
				}
			}
			redact = append(redact, values...)
		case "mode":
			m := o.value.(os.FileMode)
			if m&^os.ModePerm != 0 {
				return resolved, errors.New(errEffectModeNotPermission(cmdPath, method, m.String()))
			}
			resolved.mode = m
			resolved.hasMode = true
		case "stdin":
			resolved.stdin = o.value.([]byte)
			resolved.hasStdin = true
		case "timeout":
			d := o.value.(time.Duration)
			if d <= 0 {
				return resolved, errors.New(errEffectTimeoutNotPositive(cmdPath, method, d.String()))
			}
			resolved.timeout = d
		case "headers":
			if resolved.headers == nil {
				resolved.headers = map[string]string{}
			}
			resolved.headers[o.key] = o.value.(string)
		}
	}
	if len(redact) > 0 {
		resolved.redactor = newRedactor(redact)
	}
	return resolved, nil
}

// redactedMarker replaces every Redact value.
const redactedMarker = "«redacted»"

// newRedactor replaces the longest values first, so a value that contains
// another is hidden whole.
func newRedactor(values []string) *strings.Replacer {
	sorted := append([]string(nil), values...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	pairs := make([]string, 0, 2*len(sorted))
	for _, v := range sorted {
		pairs = append(pairs, v, redactedMarker)
	}
	return strings.NewReplacer(pairs...)
}

// scrub applies the call's Redact values to text the framework renders.
func (o effectOpts) scrub(text string) string {
	if o.redactor == nil {
		return text
	}
	return o.redactor.Replace(text)
}

// scrubErr applies the call's Redact values to an error the call returns.
func (o effectOpts) scrubErr(err error) error {
	if err == nil || o.redactor == nil {
		return err
	}
	msg := o.redactor.Replace(err.Error())
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, orig: err}
}

// redactedError is an error whose message had Redact values replaced. It
// answers errors.Is for the error it replaced but does not unwrap to it, since
// that error's message carries the values.
type redactedError struct {
	msg  string
	orig error
}

func (r *redactedError) Error() string { return r.msg }

func (r *redactedError) Is(target error) bool { return errors.Is(r.orig, target) }

// --- the structured effect log --------------------------------------------

// effectRecord is one entry in the structured effect log (§14.2).
type effectRecord struct {
	seq      int
	kind     string
	verb     string
	detail   string
	nbytes   int
	hasBytes bool
	// stdinBytes is the length of a Stdin option's data. The data itself is
	// never stored here: a record is echoed, and stdin carries secrets.
	stdinBytes int
	hasStdin   bool
	// bodyBytes is the length of an HTTP Body; like stdin, the content is
	// never stored here.
	bodyBytes     int
	hasBody       bool
	mode          os.FileMode
	hasMode       bool
	timeout       time.Duration
	resource      string
	skipIfCurrent string
	grant         string
	grantReason   string
	recorded      bool
}

func (r effectRecord) toMap() map[string]interface{} {
	m := map[string]interface{}{
		"seq":      r.seq,
		"kind":     r.kind,
		"verb":     r.verb,
		"detail":   r.detail,
		"recorded": r.recorded,
	}
	if r.hasBytes {
		m["bytes"] = r.nbytes
	}
	if r.hasStdin {
		m["stdin_bytes"] = r.stdinBytes
	}
	if r.hasBody {
		m["body_bytes"] = r.bodyBytes
	}
	if r.hasMode {
		m["mode"] = octalMode(int64(r.mode))
	}
	if r.timeout > 0 {
		m["timeout"] = r.timeout.String()
	}
	if r.resource != "" {
		m["resource"] = r.resource
	}
	if r.skipIfCurrent != "" {
		m["skip_if_current"] = r.skipIfCurrent
	}
	if r.grant != "" {
		m["grant"] = r.grant
	}
	return m
}

// render renders this record as a would-do log line (without the indent).
func (r effectRecord) render() string {
	line := fmt.Sprintf("%d. %s: %s", r.seq, r.verb, r.detail)
	if r.hasStdin {
		line += fmt.Sprintf(" (stdin: %d bytes, content withheld)", r.stdinBytes)
	}
	if r.hasBody {
		line += fmt.Sprintf(" (body: %d bytes, content withheld)", r.bodyBytes)
	}
	if r.hasMode {
		line += fmt.Sprintf(" (mode: %s)", octalMode(int64(r.mode)))
	}
	if r.timeout > 0 {
		line += fmt.Sprintf(" (timeout: %s)", r.timeout)
	}
	if r.grant != "" {
		line += fmt.Sprintf(" (granted: %s — %s)", r.grant, r.grantReason)
	}
	if r.skipIfCurrent != "" {
		line += fmt.Sprintf(" [unless resource '%s' already current]", r.skipIfCurrent)
	}
	return line
}

// dryRunHeader is the would-do log's header line. The dash is U+2014 EM DASH.
const dryRunHeader = "DRY RUN — no changes were made. Would do:"

// effectLog is the ordered effect records produced by one dispatch.
//
// TWO counters, deliberately. Would-do numbering is the numbering of the
// RENDERED lines: it feeds the log's "<N>." prefix, the «step N output» brand
// and the truncation error's "ends at step N". CacheWrites are never rendered,
// so they must never consume one of those numbers -- otherwise a
// coverage-instrumented run would silently start its preview at "2.". They get
// their own sequence instead, so every record still carries a seq.
type effectLog struct {
	records  []effectRecord
	rendered int
	cached   int
	// Claimed rendering (contract §19.7). claimed is set by
	// Effects.Recorded; handlerRendered by Effects.RenderLog. The seam skips
	// its own emission only when the handler both claimed AND rendered -- a
	// claim that never rendered is re-rendered there, so §3.5's guarantee
	// survives the claim intact.
	claimed         bool
	handlerRendered bool
	// writeSetLine is an update command's write-set line (contract §27.5),
	// rendered between the header and the first effect and taking no sequence
	// number. Empty on every command that declares no update, and set only for
	// a dry run -- a live run's write set rides the envelope instead.
	writeSetLine string
}

// seamSuppressed reports whether the handler already produced the log's bytes
// (contract §19.7).
func (l *effectLog) seamSuppressed() bool { return l.claimed && l.handlerRendered }

func (l *effectLog) append(r effectRecord) {
	l.records = append(l.records, r)
	if r.kind == CacheWrite {
		l.cached++
	} else {
		l.rendered++
	}
}

// nextSeq is the next would-do number. Pure: callers may ask without appending.
func (l *effectLog) nextSeq() int { return l.rendered + 1 }

// nextCacheSeq is the next CACHE_WRITE number, on its own counter.
func (l *effectLog) nextCacheSeq() int { return l.cached + 1 }

// render renders the would-do log. CacheWrites are never written to it.
func (l *effectLog) render() string {
	lines := []string{dryRunHeader}
	if l.writeSetLine != "" {
		lines = append(lines, "  "+l.writeSetLine)
	}
	for _, r := range l.records {
		if r.kind == CacheWrite {
			continue
		}
		lines = append(lines, "  "+r.render())
	}
	return strings.Join(lines, "\n")
}

func (l *effectLog) toList() []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(l.records))
	for _, r := range l.records {
		out = append(out, r.toMap())
	}
	return out
}

// --- the effects handle ---------------------------------------------------

// Effects is the effects handle reached as ctx.Effects().
//
// Exactly eight methods, and the set is CLOSED: there is no escape hatch that
// mints an unlisted effect, and CacheWrite has no public method at all.
type Effects struct {
	cmd              *Command
	cmdPath          string
	dryRun           bool
	log              *effectLog
	allowlist        [][]string
	grants           map[string]Grant
	mutationRecorded bool
	// httpClient sends every live HTTP request: the app's WithHTTPClient
	// client, or defaultHTTPClient.
	httpClient *http.Client
	trace      traceIdentity
	// The human stream RenderLog writes to, and the mode that makes it a
	// no-op (contract §19.7).
	out  io.Writer
	json bool
	// childStreams routes a streamed child's stdout (§19.11) and its stderr;
	// nil on the programmatic door, where a child inherits the process
	// streams.
	childStreams *childStreamRoute

	// children is every child started through Spawn in this dispatch, in
	// spawn order, for the exit step to settle (§19.11's box). Spawn may be
	// called from functions started through Go, so access is locked.
	childrenMu sync.Mutex
	children   []*spawnedChild
}

// childStreamRoute decides where the output of a child run through Spawn or
// Run(Stream(true)) goes. Its stdout (contract §19.11) is captured into the
// envelope's `output` member in machine mode; otherwise it goes to the
// dispatch's stdout, which on an owns-stdout command is part of the document.
// Its stderr goes to the dispatch's stderr. The dispatch's streams are the
// process streams on Run and the call's own capture on Test.
type childStreamRoute struct {
	machine    bool
	ownsStdout bool
	// stdout is the framework's route to the dispatch's stdout: the saved
	// descriptor while the runtime guard is armed.
	stdout io.Writer
	stderr io.Writer
	output *outputMember
}

// bindChildStreams installs the dispatch's child-stream route.
func (e *Effects) bindChildStreams(r childStreamRoute) {
	e.childStreams = &r
}

// streamTarget returns the writer a streamed child's stdout goes to, or a
// capture when it must be read into the `output` member.
func (e *Effects) streamTarget() (io.Writer, *childCapture) {
	r := e.childStreams
	switch {
	case r == nil:
		return os.Stdout, nil
	case r.ownsStdout:
		return r.stdout, nil
	case r.machine:
		return nil, &childCapture{out: r.output}
	default:
		return r.stdout, nil
	}
}

// stderrTarget returns the writer a streamed child's stderr goes to.
func (e *Effects) stderrTarget() io.Writer {
	if e.childStreams == nil {
		return os.Stderr
	}
	return e.childStreams.stderr
}

// Recorded returns the records recorded so far in this dispatch (contract
// §19.7).
//
// The shape is §14.2's, the same one §14.3's accessor returns for the whole
// run. Calling it CLAIMS the render: the framework's own end-of-dispatch
// emission is suppressed for the rest of the run, so a handler can put the
// preview where it wants it. A claim that never renders is re-rendered at the
// seam -- claiming moves the render, it can never remove it (§3.5).
//
// In machine mode claiming changes nothing: there is no human stream to order
// and the envelope's preview is unconditional either way.
func (e *Effects) Recorded() []map[string]interface{} {
	e.log.claimed = true
	return e.log.toList()
}

// RenderLog renders the would-do log in §3.2's exact form, here (contract
// §19.7).
//
// Byte-identical to what the framework would have emitted at the end of the
// dispatch -- one renderer, one record list -- so this moves the preview in
// the stream and never changes its content. Calling it also claims the render,
// which is what keeps the log from appearing twice.
//
// A no-op in machine mode (§19.7) and outside dry mode, in both cases for the
// same reason: those are exactly the runs where the framework's own
// end-of-dispatch emission produces nothing.
func (e *Effects) RenderLog() {
	e.log.claimed = true
	if e.json || !e.dryRun {
		return
	}
	e.log.handlerRendered = true
	out := e.out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintln(out, e.log.render())
}

func newEffects(cmd *Command, cmdPath string, dryRun bool, log *effectLog, allowlist [][]string, trace traceIdentity, out io.Writer, json bool) *Effects {
	grants := make(map[string]Grant, len(cmd.Grants))
	for _, g := range cmd.Grants {
		grants[g.Name] = g
	}
	return &Effects{
		cmd:        cmd,
		cmdPath:    cmdPath,
		dryRun:     dryRun,
		log:        log,
		allowlist:  allowlist,
		grants:     grants,
		httpClient: defaultHTTPClient,
		trace:      trace,
		out:        out,
		json:       json,
	}
}

// operand is a resolved carrier-accepting parameter.
type operand struct {
	value     string
	rendered  string
	unsettled bool
}

// resolveOperand resolves one carrier-accepting parameter position (any argv
// element, path, src, dst, url or content). A carrier with no scalar projection
// -- a Spawned, or a void result -- is a call-time hard error in both modes.
func (e *Effects) resolveOperand(value interface{}, method, param string) (operand, error) {
	switch v := value.(type) {
	case string:
		return operand{value: v, rendered: v}, nil
	case Unsettled:
		// The void carrier stands for nothing in either mode.
		return operand{}, errors.New(errEffectParamRejectsCarrier(e.cmdPath, method, param))
	case Spawned:
		// A Spawned has no scalar projection.
		return operand{}, errors.New(errEffectParamRejectsCarrier(e.cmdPath, method, param))
	case Completed:
		if !v.settled {
			return operand{rendered: v.brand, unsettled: true}, nil
		}
		return operand{value: v.stdout, rendered: v.stdout}, nil
	case Response:
		if !v.settled {
			return operand{rendered: v.brand, unsettled: true}, nil
		}
		text, err := decodeEffectOutput(v.body, e.cmdPath, "http")
		if err != nil {
			return operand{}, err
		}
		return operand{value: text, rendered: text}, nil
	default:
		return operand{}, errors.New(errEffectParamType(e.cmdPath, method, param, fmt.Sprintf("%T", value)))
	}
}

// contentOperand resolves write's content. The rendered form is the encoded
// byte count for a settled value, and the forwarded carrier's brand when the
// content is unsettled -- nothing produced those bytes and the framework will
// not invent a count.
func (e *Effects) contentOperand(value interface{}) ([]byte, string, error) {
	switch v := value.(type) {
	case []byte:
		return v, fmt.Sprintf("%d bytes", len(v)), nil
	case string:
		return []byte(v), fmt.Sprintf("%d bytes", len(v)), nil
	}
	op, err := e.resolveOperand(value, "write", "content")
	if err != nil {
		return nil, "", err
	}
	if op.unsettled {
		return nil, op.rendered, nil
	}
	data := []byte(op.value)
	return data, fmt.Sprintf("%d bytes", len(data)), nil
}

// resolveArgv resolves every argv element.
func (e *Effects) resolveArgv(argv []interface{}, method string) ([]operand, string, error) {
	if len(argv) == 0 {
		return nil, "", errors.New(errEffectArgvEmpty(e.cmdPath, method))
	}
	ops := make([]operand, 0, len(argv))
	rendered := make([]string, 0, len(argv))
	for i, element := range argv {
		op, err := e.resolveOperand(element, method, fmt.Sprintf("argv[%d]", i))
		if err != nil {
			return nil, "", err
		}
		ops = append(ops, op)
		rendered = append(rendered, op.rendered)
	}
	return ops, strings.Join(rendered, " "), nil
}

// isObserve does element-wise argv-prefix matching by string equality. Nothing
// else: no normalization, no globbing, no shape inference.
func (e *Effects) isObserve(ops []operand) bool {
	for _, prefix := range e.allowlist {
		if len(prefix) > len(ops) {
			continue
		}
		match := true
		for i := range prefix {
			if ops[i].unsettled || ops[i].value != prefix[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// authorize applies read-only enforcement plus grant validation, at call time.
func (e *Effects) authorize(method, kind, grant string, hasGrant bool) (*Grant, error) {
	if e.cmd.Effect == EffectReadOnly {
		return nil, errors.New(errEffectMutatingInReadOnly(e.cmdPath, method))
	}
	return e.checkGrant(kind, grant, hasGrant)
}

func (e *Effects) checkGrant(kind, grant string, hasGrant bool) (*Grant, error) {
	if !hasGrant {
		return nil, nil
	}
	declared, ok := e.grants[grant]
	if !ok {
		return nil, errors.New(errEffectGrantUndeclared(e.cmdPath, grant))
	}
	if declared.Kind != kind {
		return nil, errors.New(errEffectGrantKindMismatch(e.cmdPath, grant, declared.Kind, kind))
	}
	return &declared, nil
}

type recordSpec struct {
	kind     string
	verb     string
	detail   string
	opts     effectOpts
	grant    *Grant
	nbytes   int
	hasBytes bool
	recorded bool
}

func (e *Effects) record(spec recordSpec) effectRecord {
	rec := effectRecord{
		seq:           e.log.nextSeq(),
		kind:          spec.kind,
		verb:          spec.verb,
		detail:        spec.opts.scrub(spec.detail),
		nbytes:        spec.nbytes,
		hasBytes:      spec.hasBytes,
		stdinBytes:    len(spec.opts.stdin),
		hasStdin:      spec.opts.hasStdin,
		bodyBytes:     len(spec.opts.body),
		hasBody:       spec.opts.hasBody,
		mode:          spec.opts.mode,
		hasMode:       spec.opts.hasMode,
		timeout:       spec.opts.timeout,
		resource:      spec.opts.scrub(spec.opts.resource),
		skipIfCurrent: spec.opts.scrub(spec.opts.skipIfCurrent),
		recorded:      spec.recorded,
	}
	if spec.grant != nil {
		rec.grant = spec.grant.Name
		rec.grantReason = spec.grant.Reason
	}
	e.log.append(rec)
	return rec
}

func (e *Effects) brandFor(seq int) string {
	e.mutationRecorded = true
	return fmt.Sprintf("«step %d output»", seq)
}

// octalMode renders a mode as leading-zero octal, the form Chmod's detail and
// Write's Mode share.
func octalMode(mode int64) string {
	return "0" + strconv.FormatInt(mode, 8)
}

func (e *Effects) staleBrand(descr string) string {
	return fmt.Sprintf("«stale: %s»", descr)
}

// --- the eight methods ----------------------------------------------------

// Run runs a subprocess to completion (PROC_MUTATE), or performs an observe
// when the argv matches an app-level proc_observe_allowlist prefix.
func (e *Effects) Run(argv []interface{}, opts ...EffectOption) (_ Completed, err error) {
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(e.cmdPath, "run", opts, acceptedRun)
	if err != nil {
		return Completed{}, err
	}
	ops, joined, err := e.resolveArgv(argv, "run")
	if err != nil {
		return Completed{}, err
	}
	shown := o.scrub(joined)

	if o.observe || e.isObserve(ops) {
		// An observe changes nothing: it is legal in a read_only command, never
		// written to the would-do log, and never carries a grant.
		if o.hasGrant {
			if o.observe {
				return Completed{}, errors.New(errEffectGrantOnDeclaredRead(e.cmdPath, o.grant, "run", "observe"))
			}
			return Completed{}, errors.New(errEffectGrantOnObserve(e.cmdPath, o.grant))
		}
		if e.dryRun && e.mutationRecorded {
			return Completed{brand: e.staleBrand(shown), log: e.log, cmdPath: e.cmdPath}, nil
		}
		return e.execRun(ops, shown, o, "run")
	}

	if e.cmd.Effect == EffectReadOnly {
		return Completed{}, errors.New(errEffectRunNotAllowlisted(e.cmdPath, shown))
	}
	declared, err := e.checkGrant(ProcMutate, o.grant, o.hasGrant)
	if err != nil {
		return Completed{}, err
	}

	if e.dryRun {
		rec := e.record(recordSpec{kind: ProcMutate, verb: "run", detail: joined, opts: o, grant: declared, recorded: true})
		return Completed{brand: e.brandFor(rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
	}
	e.record(recordSpec{kind: ProcMutate, verb: "run", detail: joined, opts: o, grant: declared, recorded: false})
	return e.execRun(ops, shown, o, "run")
}

// Spawn starts a subprocess without waiting (PROC_SPAWN).
//
// Spawning is itself an effect: a dry run RECORDS the spawn instead of
// performing it, which is why no cross-process mode token exists.
func (e *Effects) Spawn(argv []interface{}, opts ...EffectOption) (_ Spawned, err error) {
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(e.cmdPath, "spawn", opts, acceptedSpawn)
	if err != nil {
		return Spawned{}, err
	}
	ops, joined, err := e.resolveArgv(argv, "spawn")
	if err != nil {
		return Spawned{}, err
	}
	declared, err := e.authorize("spawn", ProcSpawn, o.grant, o.hasGrant)
	if err != nil {
		return Spawned{}, err
	}

	if e.dryRun {
		rec := e.record(recordSpec{kind: ProcSpawn, verb: "spawn", detail: joined, opts: o, grant: declared, recorded: true})
		return Spawned{brand: e.brandFor(rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
	}
	e.record(recordSpec{kind: ProcSpawn, verb: "spawn", detail: joined, opts: o, grant: declared, recorded: false})

	settled, err := e.settledArgv(ops, "spawn")
	if err != nil {
		return Spawned{}, err
	}
	// The argv a Spawned carries is the one its errors and the exit step's
	// diagnostic name, so it is the redacted one.
	joined = o.scrub(joined)
	cmd := exec.Command(settled[0], settled[1:]...)
	cmd.Dir = o.cwd
	cmd.Env = traceChildEnv(mergedEnv(o.env), e.trace)
	if o.hasStdin {
		cmd.Stdin = bytes.NewReader(o.stdin)
	}
	cmd.Stderr = e.stderrTarget()
	target, capture := e.streamTarget()
	if capture == nil {
		cmd.Stdout = target
		if err := cmd.Start(); err != nil {
			return Spawned{}, err
		}
		child := e.trackChild(cmd, joined, nil)
		return Spawned{settled: true, pid: cmd.Process.Pid, argv: joined, cmdPath: e.cmdPath, child: child}, nil
	}
	// A captured spawn is read concurrently, through a pipe the framework
	// owns, and drained no later than Wait or the exit step (§19.11).
	pr, pw, err := os.Pipe()
	if err != nil {
		return Spawned{}, err
	}
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return Spawned{}, err
	}
	pw.Close()
	drained := make(chan struct{})
	capture.out.captures.Add(1)
	go func() {
		defer capture.out.captures.Done()
		defer close(drained)
		defer pr.Close()
		io.Copy(capture, pr)
		capture.close()
	}()
	child := e.trackChild(cmd, joined, drained)
	return Spawned{settled: true, pid: cmd.Process.Pid, argv: joined, cmdPath: e.cmdPath, child: child}, nil
}

// trackChild records a started child for the exit step to settle.
func (e *Effects) trackChild(cmd *exec.Cmd, argv string, drained chan struct{}) *spawnedChild {
	c := startChildReaper(cmd, argv, drained)
	e.childrenMu.Lock()
	e.children = append(e.children, c)
	e.childrenMu.Unlock()
	return c
}

// settleChildren settles, in spawn order, every child this dispatch started
// and the handler neither waited on nor killed (contract §19.11's box). It
// returns one diagnostic per child that had to be killed.
func (e *Effects) settleChildren() []string {
	e.childrenMu.Lock()
	children := append([]*spawnedChild(nil), e.children...)
	e.childrenMu.Unlock()
	var killed []string
	for _, c := range children {
		if msg := c.settle(); msg != "" {
			killed = append(killed, msg)
		}
	}
	return killed
}

// Write writes bytes to a path (FILE_WRITE).
func (e *Effects) Write(path interface{}, content interface{}, opts ...EffectOption) (_ Unsettled, err error) {
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(e.cmdPath, "write", opts, acceptedWrite)
	if err != nil {
		return Unsettled{}, err
	}
	pathOp, err := e.resolveOperand(path, "write", "path")
	if err != nil {
		return Unsettled{}, err
	}
	data, renderedContent, err := e.contentOperand(content)
	if err != nil {
		return Unsettled{}, err
	}
	detail := fmt.Sprintf("%s (%s)", pathOp.rendered, renderedContent)
	declared, err := e.authorize("write", FileWrite, o.grant, o.hasGrant)
	if err != nil {
		return Unsettled{}, err
	}
	spec := recordSpec{kind: FileWrite, verb: "write", detail: detail, opts: o, grant: declared}
	if data != nil {
		spec.nbytes = len(data)
		spec.hasBytes = true
	}

	if e.dryRun {
		spec.recorded = true
		rec := e.record(spec)
		return Unsettled{brand: e.brandFor(rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
	}
	rec := e.record(spec)
	target, err := e.settled(pathOp, "write", "path")
	if err != nil {
		return Unsettled{}, err
	}
	if o.hasMode {
		err = writeFileWithMode(target, data, o.mode)
	} else {
		err = os.WriteFile(target, data, 0o644)
	}
	if err != nil {
		return Unsettled{}, err
	}
	return Unsettled{brand: fmt.Sprintf("«step %d output»", rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
}

// writeFileWithMode writes data to path, leaving it with mode whether it was
// created or already existed. The mode is set before the content is written.
func writeFileWithMode(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Mkdir creates a directory, parents included; an already-existing directory is
// not an error.
func (e *Effects) Mkdir(path interface{}, opts ...EffectOption) (Unsettled, error) {
	return e.pathEffect("mkdir", path, opts, func(p string) error {
		return os.MkdirAll(p, 0o755)
	})
}

// Remove removes a file, a symlink or a directory tree recursively; a missing
// path is not an error.
func (e *Effects) Remove(path interface{}, opts ...EffectOption) (Unsettled, error) {
	return e.pathEffect("remove", path, opts, os.RemoveAll)
}

// Rename moves/renames a path (FILE_WRITE).
func (e *Effects) Rename(src interface{}, dst interface{}, opts ...EffectOption) (_ Unsettled, err error) {
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(e.cmdPath, "rename", opts, acceptedPath)
	if err != nil {
		return Unsettled{}, err
	}
	srcOp, err := e.resolveOperand(src, "rename", "src")
	if err != nil {
		return Unsettled{}, err
	}
	dstOp, err := e.resolveOperand(dst, "rename", "dst")
	if err != nil {
		return Unsettled{}, err
	}
	detail := fmt.Sprintf("%s -> %s", srcOp.rendered, dstOp.rendered)
	declared, err := e.authorize("rename", FileWrite, o.grant, o.hasGrant)
	if err != nil {
		return Unsettled{}, err
	}
	spec := recordSpec{kind: FileWrite, verb: "rename", detail: detail, opts: o, grant: declared}

	if e.dryRun {
		spec.recorded = true
		rec := e.record(spec)
		return Unsettled{brand: e.brandFor(rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
	}
	rec := e.record(spec)
	from, err := e.settled(srcOp, "rename", "src")
	if err != nil {
		return Unsettled{}, err
	}
	to, err := e.settled(dstOp, "rename", "dst")
	if err != nil {
		return Unsettled{}, err
	}
	if err := os.Rename(from, to); err != nil {
		return Unsettled{}, err
	}
	return Unsettled{brand: fmt.Sprintf("«step %d output»", rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
}

// Chmod changes a path's mode (FILE_WRITE). The mode renders in the log as
// leading-zero octal.
func (e *Effects) Chmod(path interface{}, mode int, opts ...EffectOption) (_ Unsettled, err error) {
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(e.cmdPath, "chmod", opts, acceptedPath)
	if err != nil {
		return Unsettled{}, err
	}
	pathOp, err := e.resolveOperand(path, "chmod", "path")
	if err != nil {
		return Unsettled{}, err
	}
	detail := fmt.Sprintf("%s %s", pathOp.rendered, octalMode(int64(mode)))
	declared, err := e.authorize("chmod", FileWrite, o.grant, o.hasGrant)
	if err != nil {
		return Unsettled{}, err
	}
	spec := recordSpec{kind: FileWrite, verb: "chmod", detail: detail, opts: o, grant: declared}

	if e.dryRun {
		spec.recorded = true
		rec := e.record(spec)
		return Unsettled{brand: e.brandFor(rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
	}
	rec := e.record(spec)
	target, err := e.settled(pathOp, "chmod", "path")
	if err != nil {
		return Unsettled{}, err
	}
	if err := os.Chmod(target, os.FileMode(mode)); err != nil {
		return Unsettled{}, err
	}
	return Unsettled{brand: fmt.Sprintf("«step %d output»", rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
}

// HTTP performs a network request (NET_MUTATE), or a declared read when the
// call carries Read().
func (e *Effects) HTTP(method string, url interface{}, opts ...EffectOption) (_ Response, err error) {
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(e.cmdPath, "http", opts, acceptedHTTP)
	if err != nil {
		return Response{}, err
	}
	urlOp, err := e.resolveOperand(url, "http", "url")
	if err != nil {
		return Response{}, err
	}
	detail := fmt.Sprintf("%s %s", method, urlOp.rendered)

	if o.read {
		// A declared read changes nothing: it is legal in a read_only command,
		// never written to the would-do log, and never carries a grant.
		if o.hasGrant {
			return Response{}, errors.New(errEffectGrantOnDeclaredRead(e.cmdPath, o.grant, "http", "read"))
		}
		if e.dryRun && e.mutationRecorded {
			return Response{brand: e.staleBrand(o.scrub(detail)), log: e.log, cmdPath: e.cmdPath}, nil
		}
		target, err := e.settled(urlOp, "http", "url")
		if err != nil {
			return Response{}, err
		}
		return e.execHTTP(method, target, o)
	}
	declared, err := e.authorize("http", NetMutate, o.grant, o.hasGrant)
	if err != nil {
		return Response{}, err
	}

	if e.dryRun {
		rec := e.record(recordSpec{kind: NetMutate, verb: "net", detail: detail, opts: o, grant: declared, recorded: true})
		return Response{brand: e.brandFor(rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
	}
	e.record(recordSpec{kind: NetMutate, verb: "net", detail: detail, opts: o, grant: declared, recorded: false})
	target, err := e.settled(urlOp, "http", "url")
	if err != nil {
		return Response{}, err
	}
	return e.execHTTP(method, target, o)
}

// --- shared execution paths -----------------------------------------------

func (e *Effects) pathEffect(verb string, path interface{}, opts []EffectOption, perform func(string) error) (_ Unsettled, err error) {
	var o effectOpts
	defer func() { err = o.scrubErr(err) }()
	o, err = parseEffectOptions(e.cmdPath, verb, opts, acceptedPath)
	if err != nil {
		return Unsettled{}, err
	}
	pathOp, err := e.resolveOperand(path, verb, "path")
	if err != nil {
		return Unsettled{}, err
	}
	declared, err := e.authorize(verb, FileWrite, o.grant, o.hasGrant)
	if err != nil {
		return Unsettled{}, err
	}
	spec := recordSpec{kind: FileWrite, verb: verb, detail: pathOp.rendered, opts: o, grant: declared}

	if e.dryRun {
		spec.recorded = true
		rec := e.record(spec)
		return Unsettled{brand: e.brandFor(rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
	}
	rec := e.record(spec)
	target, err := e.settled(pathOp, verb, "path")
	if err != nil {
		return Unsettled{}, err
	}
	if err := perform(target); err != nil {
		return Unsettled{}, err
	}
	return Unsettled{brand: fmt.Sprintf("«step %d output»", rec.seq), log: e.log, cmdPath: e.cmdPath}, nil
}

// settled is the fail-closed backstop: an unsettled operand only survives in
// dry mode, where nothing executes.
func (e *Effects) settled(op operand, method, param string) (string, error) {
	if op.unsettled {
		return "", errors.New(errEffectParamRejectsCarrier(e.cmdPath, method, param))
	}
	return op.value, nil
}

func (e *Effects) settledArgv(ops []operand, method string) ([]string, error) {
	out := make([]string, 0, len(ops))
	for i, op := range ops {
		if op.unsettled {
			return nil, errors.New(errEffectParamRejectsCarrier(e.cmdPath, method, fmt.Sprintf("argv[%d]", i)))
		}
		out = append(out, op.value)
	}
	return out, nil
}

// mergedEnv merges env OVER the inherited environment, never replacing it.
func mergedEnv(env map[string]string) []string {
	if env == nil {
		return nil
	}
	base := os.Environ()
	overrides := make(map[string]string, len(env))
	for k, v := range env {
		overrides[k] = v
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		if eq := strings.IndexByte(entry, '='); eq >= 0 {
			if _, overridden := overrides[entry[:eq]]; overridden {
				continue
			}
		}
		out = append(out, entry)
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+overrides[k])
	}
	return out
}

func (e *Effects) execRun(ops []operand, joined string, o effectOpts, method string) (Completed, error) {
	argv, err := e.settledArgv(ops, method)
	if err != nil {
		return Completed{}, err
	}
	runCtx := context.Background()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, o.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = o.cwd
	cmd.Env = traceChildEnv(mergedEnv(o.env), e.trace)
	if o.hasStdin {
		cmd.Stdin = bytes.NewReader(o.stdin)
	}
	var timedOut atomic.Bool
	if o.timeout > 0 {
		cmd.Cancel = func() error {
			err := cmd.Process.Kill()
			if err == nil {
				timedOut.Store(true)
			}
			return err
		}
		// A grandchild holding the child's output pipes open would keep Run
		// waiting past the kill; this bounds that wait.
		cmd.WaitDelay = timeoutWaitDelay
	}

	var outBuf, errBuf bytes.Buffer
	var capture *childCapture
	if o.stream {
		var target io.Writer
		target, capture = e.streamTarget()
		if capture != nil {
			// A run's child has finished writing before Run returns (§19.11).
			cmd.Stdout = capture
		} else {
			cmd.Stdout = target
		}
		cmd.Stderr = e.stderrTarget()
	} else {
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
	}
	runErr := cmd.Run()
	if capture != nil {
		capture.close()
	}
	if timedOut.Load() {
		return Completed{}, errors.New(errEffectTimedOut(e.cmdPath, method, joined, o.timeout.String()))
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return Completed{}, runErr
	}
	code := cmd.ProcessState.ExitCode()

	out, errText := "", ""
	if !o.stream {
		out, err = decodeEffectOutput(outBuf.Bytes(), e.cmdPath, method)
		if err != nil {
			return Completed{}, err
		}
		errText, err = decodeEffectOutput(errBuf.Bytes(), e.cmdPath, method)
		if err != nil {
			return Completed{}, err
		}
	}
	if o.check && code != 0 {
		return Completed{}, errors.New(errEffectRunFailed(e.cmdPath, method, joined, code))
	}
	return Completed{settled: true, exitCode: code, stdout: out, stderr: errText}, nil
}

// defaultHTTPClientTimeout bounds every request sent through the framework's
// own client, so a request that never answers cannot hang a command forever.
const defaultHTTPClientTimeout = 60 * time.Second

// defaultHTTPClient is the client of an app that declares none through
// WithHTTPClient.
var defaultHTTPClient = &http.Client{Timeout: defaultHTTPClientTimeout}

func (e *Effects) execHTTP(method, url string, o effectOpts) (Response, error) {
	var reqBody io.Reader
	if o.body != nil {
		reqBody = bytes.NewReader(o.body)
	}
	reqCtx := context.Background()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, o.timeout)
		defer cancel()
	}
	timedOut := func() error {
		if o.timeout > 0 && errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return errors.New(errEffectHTTPTimedOut(e.cmdPath, method, o.scrub(url), o.timeout.String()))
		}
		return nil
	}
	req, err := http.NewRequestWithContext(reqCtx, method, url, reqBody)
	if err != nil {
		return Response{}, err
	}
	for k, v := range o.headers {
		req.Header.Set(k, v)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		if te := timedOut(); te != nil {
			return Response{}, te
		}
		return Response{}, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		if te := timedOut(); te != nil {
			return Response{}, te
		}
		return Response{}, err
	}
	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	if o.check && (resp.StatusCode < 200 || resp.StatusCode > 299) {
		return Response{}, errors.New(errEffectHTTPFailed(e.cmdPath, method, o.scrub(url), resp.StatusCode))
	}
	return Response{settled: true, status: resp.StatusCode, body: payload, headers: headers}, nil
}

// decodeEffectOutput decodes captured output as UTF-8 strictly, dropping one
// trailing newline.
func decodeEffectOutput(data []byte, cmdPath, method string) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if !utf8.Valid(data) {
		return "", errors.New(errEffectOutputNotUTF8(cmdPath, method))
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}

// --- registration-time validation -----------------------------------------

// validateGrants validates a command's grant declarations at registration time.
func validateGrants(cmdName string, grants []Grant) []Grant {
	seen := make(map[string]bool, len(grants))
	resolved := make([]Grant, 0, len(grants))
	for _, g := range grants {
		if !isKebabName(g.Name) {
			panic(errGrantNameInvalid(cmdName, g.Name))
		}
		if seen[g.Name] {
			panic(errGrantDuplicate(cmdName, g.Name))
		}
		if strings.TrimSpace(g.Reason) == "" {
			panic(errGrantReasonEmpty(cmdName, g.Name))
		}
		valid := false
		for _, k := range grantableKinds {
			if g.Kind == k {
				valid = true
				break
			}
		}
		if !valid {
			panic(errGrantKindInvalid(cmdName, g.Name, g.Kind))
		}
		seen[g.Name] = true
		resolved = append(resolved, g)
	}
	return resolved
}

// --- the confirm protocol -------------------------------------------------

// stdinIsInteractive reports whether stdin is a TTY. Zero-dependency by
// construction: a character device is the signal, so no golang.org/x/term.
//
// The null device is excluded explicitly. It IS a character device, so the mode
// check alone reads `myapp cmd < /dev/null` -- and every subprocess launched
// with a null stdin, which is what CI runners and test harnesses do -- as
// interactive, where Python's isatty() and Node's isTTY both report false. That
// divergence made the same invocation prompt on Go and hard-error on the other
// two. os.SameFile against os.DevNull is stdlib-only and portable (the constant
// is "NUL" on Windows), so the zero-dependency property is preserved.
func stdinIsInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if nullInfo, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, nullInfo) {
		return false
	}
	return true
}

// ConfirmIO is the stdin side of the confirm protocol, isolated so a test
// harness can drive it. Both members are required when it is installed.
//
// The TypeScript twin is the ConfirmIO interface in confirm.ts and the Python
// twin is the _ConfirmIO class; the two members mean the same thing in all
// three. Installing one changes WHERE the answer comes from, never WHETHER the
// protocol runs -- it is not a bypass, and there is no bypass.
type ConfirmIO struct {
	// IsInteractive reports whether the answer channel is a terminal. The
	// framework's own answer is stdinIsInteractive().
	IsInteractive func() bool
	// In is where the answer line is read from.
	In io.Reader
}

// SetConfirmIO swaps the stdin side of the confirm protocol. Passing nil
// restores the real stdin reader.
//
// Test-only surface, beside Test(), EffectLog() and SetExitHook(). Go's twins
// in the other two implementations are package-internal (TypeScript's
// setConfirmIO is never re-exported from index.ts; Python's is the
// underscore-named App._set_confirm_io), but Go has no package-private
// visibility that another package -- the conformance harness above all -- can
// still reach, so this one is exported and documented as test-only.
func (a *App) SetConfirmIO(io *ConfirmIO) {
	a.confirmIO = io
}

// confirmConsequential is the framework-owned confirm protocol. It fires before
// dispatching a command that DECLARES ITSELF consequential, on the real CLI
// path, when neither --dry-run nor --approve-consequential was passed. A plain
// mutating command never prompts: classification answers "should a dry run
// record rather than execute?", which is a different question from "are these
// effects worth interrupting someone for?". It never fires on the programmatic
// paths (Test/Call/invoke/MCP), which have no TTY contract and would hang.
//
// A consequential PASSTHROUGH is not exempt: the framework knows LESS about
// what is about to happen, not more.
func (a *invocation) confirmConsequential(cmd *Command, cmdPath string) {
	interactive, in := stdinIsInteractive(), io.Reader(os.Stdin)
	if a.confirmIO != nil {
		interactive, in = a.confirmIO.IsInteractive(), a.confirmIO.In
	}
	switch a.confirmDecision(cmd, cmdPath, interactive, in, os.Stderr) {
	case confirmNonInteractive:
		fmt.Fprintln(os.Stderr, errConfirmNonInteractive)
		os.Exit(1)
	case confirmDeclined:
		fmt.Fprintln(os.Stderr, errConfirmDeclined)
		os.Exit(1)
	}
}

// confirmDecision is the confirm protocol's testable core: it decides, prompts
// when a decision is needed, and never exits.
type confirmOutcome int

const (
	confirmProceed confirmOutcome = iota
	confirmNonInteractive
	confirmDeclined
)

func (a *invocation) confirmDecision(cmd *Command, cmdPath string, interactive bool, in io.Reader, prompt io.Writer) confirmOutcome {
	if !cmd.Consequential {
		return confirmProceed
	}
	if a.reserved.dryRun || a.reserved.approveConsequential {
		return confirmProceed
	}
	if !interactive {
		return confirmNonInteractive
	}
	fmt.Fprint(prompt, promptConfirmConsequential(cmdPath))
	answer, _ := readConfirmLine(in)
	if answer != "y" && answer != "Y" {
		return confirmDeclined
	}
	return confirmProceed
}

// readConfirmLine reads one line from r. Exactly "y" or "Y" proceeds; anything
// else -- including empty input and EOF -- declines.
func readConfirmLine(r io.Reader) (string, error) {
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				break
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			return strings.TrimSuffix(string(buf), "\r"), err
		}
	}
	return strings.TrimSuffix(string(buf), "\r"), nil
}

// --- App-side plumbing ----------------------------------------------------

// armEffects arms the effects handle for one dispatch (the runtime seal).
//
// Called at EVERY newContext site that dispatches a handler, so there is no path
// on which ctx.Effects() is missing or a carrier escapes unpoisoned. The log is
// the invocation's own, created with it, so pre-handler CACHE_WRITEs (coverage
// shards) land in the same dispatch's log.
func (a *invocation) armEffects(cmd *Command, cmdPath string, dryRun bool, out io.Writer) *Effects {
	e := newEffects(cmd, cmdPath, dryRun, a.effects, a.procObserveAllowlist,
		traceIdentity{
			app:                  a.Name,
			version:              a.Version,
			command:              cmdPath,
			hasCommand:           true,
			dryRun:               dryRun,
			machineMode:          a.reserved.json,
			quiet:                a.reserved.quiet,
			verbose:              a.reserved.verbose,
			approveConsequential: a.reserved.approveConsequential,
			effect:               cmd.Effect,
		}, out, a.reserved.json)
	if a.httpClient != nil {
		e.httpClient = a.httpClient
	}
	return e
}

// recordCacheWrite records a framework-blessed CACHE_WRITE.
//
// The closed list of sites: the test-coverage shards and the test-coverage
// manifest. CACHE_WRITEs have no public method,
// never appear in the would-do log, never trip read-only enforcement, and
// EXECUTE even in dry mode -- which is why they always carry recorded: false.
func (l *effectLog) recordCacheWrite(path string) {
	l.append(effectRecord{
		seq:      l.nextCacheSeq(),
		kind:     CacheWrite,
		verb:     "cache",
		detail:   path,
		recorded: false,
	})
}

// EffectLog returns the structured effect records of the most recently
// finished dispatch.
//
// Public API (contract §14.3's amendment). It carries the same records as the
// envelope's preview (§19.3), so it is part of the surface consumers may rely
// on and it is in the api-surface catalog rather than excluded from it. The
// records are populated in both modes, so a live run's effects read as readily
// as a dry run's. Dispatches running at the same time each write their own log;
// the one that finishes last is the one returned here.
func (a *App) EffectLog() []map[string]interface{} {
	a.lastEffectsMu.Lock()
	log := a.lastEffects
	a.lastEffectsMu.Unlock()
	if log == nil {
		return []map[string]interface{}{}
	}
	return log.toList()
}
