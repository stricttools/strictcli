package strictcli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// WithScratchDir declares that the command's handler may ask for a scratch
// directory through Context.ScratchDir: a private directory the framework
// makes outside the repository the first time the handler asks for it, and
// removes when the dispatch ends. It is the place for what the programs a
// handler runs write for themselves (a package manager's cache, a tool's
// logs), so a read_only command can run them, and a --dry-run can run them
// for real, without any of it reaching the repository or the user's own
// caches.
//
// Making and removing the directory is the framework's own act: it is never
// an effect, never refused in a read_only command, and never written to the
// would-do log. The directory is not a place for the command's results:
// everything in it is gone when the dispatch ends. What the handler itself
// writes there through the effects handle is an effect like any other write.
func WithScratchDir() CmdOption {
	return func(c *Command) {
		c.scratchDir = true
	}
}

// scratch is one dispatch's scratch directory: declared or not, and made on
// the first request.
type scratch struct {
	declared bool
	mu       sync.Mutex
	dir      string
}

// scratchRoot is where every scratch directory is made: a literal path under
// the home directory, as the trace store's is, so every run on one machine
// agrees on it without consulting the environment.
func scratchRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "strictcli", "scratch"), nil
}

// ScratchDir is the dispatch's scratch directory, made on the first call and
// the same on every later one. A command that does not declare
// WithScratchDir is refused: what a handler may write outside the effects
// handle is declared, never assumed.
func (c *Context) ScratchDir() (string, error) {
	if !c.scratch.declared {
		return "", fmt.Errorf("command %q asked for a scratch directory and declares none; declare one with WithScratchDir", c.commandName)
	}
	c.scratch.mu.Lock()
	defer c.scratch.mu.Unlock()
	if c.scratch.dir != "" {
		return c.scratch.dir, nil
	}
	root, err := scratchRoot()
	if err != nil {
		return "", fmt.Errorf("command %q: the scratch directory cannot be made, since the home directory cannot be found: %w", c.commandName, err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("command %q: making the scratch directory under %s: %w", c.commandName, root, err)
	}
	dir, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return "", fmt.Errorf("command %q: making the scratch directory under %s: %w", c.commandName, root, err)
	}
	c.scratch.dir = dir
	return dir, nil
}

// removeScratch removes the dispatch's scratch directory when one was made.
// It runs once the handler's children are settled, so nothing is writing to
// it any more.
func (c *Context) removeScratch() error {
	if c == nil {
		return nil
	}
	c.scratch.mu.Lock()
	defer c.scratch.mu.Unlock()
	if c.scratch.dir == "" {
		return nil
	}
	dir := c.scratch.dir
	c.scratch.dir = ""
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the scratch directory %s could not be removed after the command: %v; remove it by hand", dir, err)
	}
	return nil
}
