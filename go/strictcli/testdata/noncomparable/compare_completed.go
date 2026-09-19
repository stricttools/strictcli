package noncomparable

import "github.com/stricttools/strictcli/go/strictcli"

// compareCompleted must NOT compile: Completed's `_ [0]func()` field makes the
// struct non-comparable. Every other field of Completed is comparable, so this
// file compiles cleanly the moment the field is dropped.
func compareCompleted(a, b strictcli.Completed) bool {
	return a == b
}
