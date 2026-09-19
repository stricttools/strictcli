package noncomparable

import "github.com/stricttools/strictcli/go/strictcli"

// compareSpawned must NOT compile: Spawned's `_ [0]func()` field makes the
// struct non-comparable. Every other field of Spawned is comparable (the
// *exec.Cmd is a pointer), so this file compiles cleanly the moment the field
// is dropped.
func compareSpawned(a, b strictcli.Spawned) bool {
	return a == b
}
