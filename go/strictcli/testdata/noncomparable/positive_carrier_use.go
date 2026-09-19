package noncomparable

import "github.com/stricttools/strictcli/go/strictcli"

// carrierUse is the POSITIVE half of the pin: the four carriers are perfectly
// usable in every non-comparing position (parameters, returns, interface
// boxing). It must produce ZERO diagnostics, which is what proves the driver
// test attributes errors per file rather than blaming the whole package.
func carrierUse(u strictcli.Unsettled, c strictcli.Completed, s strictcli.Spawned, r strictcli.Response) []any {
	return []any{u, c, s, r}
}
