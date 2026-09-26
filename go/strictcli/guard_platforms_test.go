package strictcli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The runtime guard redirects at the operating-system level on every platform
// that has one (contract §19.12's box): the redirect's platform files must
// build and vet where the standard syscall package offers no dup2 (Solaris,
// illumos, AIX) and on Windows, which swaps the standard output handle.
func TestTheGuardBuildsAndVetsOnEveryRedirectPlatform(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-platform vet runs the go toolchain")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	targets := []struct{ goos, goarch string }{
		{"windows", "amd64"},
		{"solaris", "amd64"},
		{"illumos", "amd64"},
		{"aix", "ppc64"},
	}
	for _, tg := range targets {
		t.Run(tg.goos, func(t *testing.T) {
			cmd := exec.Command(gobin, "vet", ".")
			cmd.Env = append(os.Environ(), "GOOS="+tg.goos, "GOARCH="+tg.goarch, "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("GOOS=%s go vet failed: %v\n%s", tg.goos, err, strings.TrimSpace(string(out)))
			}
		})
	}
}
