package strictcli

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// emptyProjectRoot is a dedicated, empty project root. Checks that statically
// analyse the consumer's sources (effects-bypass) walk this, so it must not be
// a shared scratch directory like /tmp.
var emptyProjectRoot = func() string {
	dir := filepath.Join("_fixtures", "empty_project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}()

// dropBuiltinCheckProviders strips strictcli's own built-in check providers
// from an app.
//
// Enabling the check system also registers the built-in effects-bypass lint.
// Tests that assert on a specific check inventory (counts, --list output,
// result ordering) drop it so they keep testing the runner rather than the
// framework's own checks; dedicated tests cover the built-in itself.
func dropBuiltinCheckProviders(app *App) *App {
	kept := app.checkProviders[:0:0]
	for _, p := range app.checkProviders {
		name := runtime.FuncForPC(reflect.ValueOf(p).Pointer()).Name()
		if strings.Contains(name, "effectsBypassProvider") {
			continue
		}
		kept = append(kept, p)
	}
	app.checkProviders = kept
	app.providerMaterialized = false
	app.providerMaterializedCwd = ""
	for name := range app.providerSourcedNames {
		delete(app.checkDefs, name)
		for i, n := range app.checkOrder {
			if n == name {
				app.checkOrder = append(app.checkOrder[:i], app.checkOrder[i+1:]...)
				break
			}
		}
	}
	app.providerSourcedNames = nil
	return app
}

func TestBuiltinEffectsBypassProviderIsRegisteredWithChecks(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	app.SetCheckContext(func(*Context) (CheckContext, error) { return &testCheckContext{root: emptyProjectRoot}, nil })
	r := app.Test([]string{"check", "--list"})
	if !strings.Contains(r.Stdout, "effects-bypass") {
		t.Fatalf("expected the built-in effects-bypass check in --list, got %q", r.Stdout)
	}
}
