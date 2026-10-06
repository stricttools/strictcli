package strictcli

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// floatVectorsPath locates testdata/float_vectors.json relative to this
// source file via runtime.Caller, so the test works regardless of the process
// working directory.
func floatVectorsPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile) // .../go/strictcli
	return filepath.Join(dir, "testdata", "float_vectors.json")
}

type floatVector struct {
	Bits string `json:"bits"`
	SCF  string `json:"scf"`
}

type floatVectorDoc struct {
	Count   int           `json:"count"`
	Vectors []floatVector `json:"vectors"`
}

// TestFormatFloatCanonicalVectors replays the committed cross-language SCF
// vectors (generated from the Python reference formatter) and asserts the Go
// formatter reproduces every recorded string byte-for-byte, proving
// cross-language parity.
func TestFormatFloatCanonicalVectors(t *testing.T) {
	data, err := os.ReadFile(floatVectorsPath(t))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var doc floatVectorDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if len(doc.Vectors) == 0 {
		t.Fatal("no vectors loaded")
	}
	if len(doc.Vectors) != doc.Count {
		t.Fatalf("count mismatch: header says %d, got %d vectors", doc.Count, len(doc.Vectors))
	}
	for _, v := range doc.Vectors {
		bits, err := strconv.ParseUint(v.Bits, 16, 64)
		if err != nil {
			t.Fatalf("bad bits %q: %v", v.Bits, err)
		}
		x := math.Float64frombits(bits)
		got := formatFloatCanonical(x)
		if got != v.SCF {
			t.Errorf("bits=%s go SCF=%q != recorded %q", v.Bits, got, v.SCF)
		}
	}
}
