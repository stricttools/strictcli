package noncomparable

import "github.com/stricttools/strictcli/go/strictcli"

// compareResponse must NOT compile: Response's `_ [0]func()` field makes the
// struct non-comparable. Because the field is declared FIRST, it is the one the
// compiler names -- dropping it would change the diagnostic to one about
// `body []byte`, which the driver test rejects.
func compareResponse(a, b strictcli.Response) bool {
	return a == b
}
