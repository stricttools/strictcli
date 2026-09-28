package strictcli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// int is 32 bits wide on these targets, so an untyped constant beyond 2^31
// compared against an int fails to compile there: the package, and so every
// program built with it, must build and vet where int is narrow.
func TestThePackageBuildsAndVetsWhereIntIs32Bits(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-architecture vet runs the go toolchain")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	for _, goarch := range []string{"386", "arm", "mips", "mipsle"} {
		t.Run(goarch, func(t *testing.T) {
			cmd := exec.Command(gobin, "vet", ".")
			cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("GOARCH=%s go vet failed: %v\n%s", goarch, err, strings.TrimSpace(string(out)))
			}
		})
	}
}
