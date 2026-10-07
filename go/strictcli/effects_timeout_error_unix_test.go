//go:build unix

package strictcli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A run, a spawn wait, and an HTTP request that exceed their Timeout return
// an error matching ErrTimedOut, so a caller tells a timeout from any other
// failure without reading the message.
func TestATimedOutEffectMatchesErrTimedOut(t *testing.T) {
	var runErr, waitErr, redactedErr, failedErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, runErr = ctx.Effects().Run([]interface{}{"sleep", "30"}, Timeout(200*time.Millisecond))
		s := mustSpawn(ctx, "sleep", "30")
		_, waitErr = s.Wait(Timeout(200 * time.Millisecond))
		// A redacted message still matches.
		_, redactedErr = ctx.Effects().Run([]interface{}{"sleep", "30"}, Timeout(200*time.Millisecond), Redact("30"))
		// A failure that is not a timeout does not.
		_, failedErr = ctx.Effects().Run([]interface{}{"strictcli-no-such-program"}, Timeout(10*time.Second))
		return Exit(0)
	})
	app.Test([]string{"go"})
	for name, err := range map[string]error{"run": runErr, "spawn wait": waitErr, "redacted run": redactedErr} {
		if !errors.Is(err, ErrTimedOut) {
			t.Errorf("%s: %v does not match ErrTimedOut", name, err)
		}
	}
	if failedErr == nil || errors.Is(failedErr, ErrTimedOut) {
		t.Errorf("a run that did not start matches ErrTimedOut: %v", failedErr)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	var httpErr error
	httpApp, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		_, httpErr = ctx.Effects().HTTP("GET", srv.URL+"/slow", Timeout(50*time.Millisecond))
		return Exit(0)
	})
	httpApp.Test([]string{"go"})
	if !errors.Is(httpErr, ErrTimedOut) {
		t.Errorf("http: %v does not match ErrTimedOut", httpErr)
	}
}
