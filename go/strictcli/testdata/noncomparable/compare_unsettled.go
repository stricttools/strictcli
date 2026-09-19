package noncomparable

import "github.com/stricttools/strictcli/go/strictcli"

// compareUnsettled must NOT compile: Unsettled's `_ [0]func()` field makes the
// struct non-comparable.
func compareUnsettled(a, b strictcli.Unsettled) bool {
	return a == b
}
