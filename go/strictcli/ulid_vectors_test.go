package strictcli

// Replays the committed strict-ULID vectors against the Go implementation.
//
// The vectors live at testdata/ulid_vectors.json and are authored there --
// not derived from any implementation. The Python and TypeScript suites replay
// the same file, which pins the profile (docs/process-trace-store.md,
// "Identifiers") for all three minters.

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type ulidEncodeVector struct {
	Name      string `json:"name"`
	MS        int64  `json:"ms"`
	RandomHex string `json:"random_hex"`
	ULID      string `json:"ulid"`
}

type ulidParseVector struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Valid bool   `json:"valid"`
	MS    int64  `json:"ms"`
}

type ulidVectorDoc struct {
	EncodeCount   int                `json:"encode_vector_count"`
	ParseCount    int                `json:"parse_vector_count"`
	EncodeVectors []ulidEncodeVector `json:"encode_vectors"`
	ParseVectors  []ulidParseVector  `json:"parse_vectors"`
}

// ulidVectorsPath locates testdata/ulid_vectors.json relative to this source
// file via runtime.Caller, so the test works regardless of the process working
// directory.
func ulidVectorsPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "ulid_vectors.json")
}

func loadULIDVectors(t *testing.T) ulidVectorDoc {
	t.Helper()
	data, err := os.ReadFile(ulidVectorsPath(t))
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var doc ulidVectorDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(doc.EncodeVectors) != doc.EncodeCount || doc.EncodeCount == 0 {
		t.Fatalf("encode vector count %d does not match %d", len(doc.EncodeVectors), doc.EncodeCount)
	}
	if len(doc.ParseVectors) != doc.ParseCount || doc.ParseCount == 0 {
		t.Fatalf("parse vector count %d does not match %d", len(doc.ParseVectors), doc.ParseCount)
	}
	return doc
}

func TestULIDEncodeVectors(t *testing.T) {
	for _, vec := range loadULIDVectors(t).EncodeVectors {
		randomness, err := hex.DecodeString(vec.RandomHex)
		if err != nil {
			t.Fatalf("%s: bad random_hex: %v", vec.Name, err)
		}
		if len(randomness) != 10 {
			t.Fatalf("%s: random_hex is %d bytes, want 10", vec.Name, len(randomness))
		}
		got := ulidEncode(vec.MS, randomness)
		if got != vec.ULID {
			t.Errorf("%s: encoded %q, want %q", vec.Name, got, vec.ULID)
		}
		if len(got) != 26 {
			t.Errorf("%s: encoding is %d characters, want 26", vec.Name, len(got))
		}
		// Every encoding round-trips to the millisecond it carries.
		ms, ok := ulidTimestamp(got)
		if !ok || ms != vec.MS {
			t.Errorf("%s: round-trip yielded (%d, %v), want (%d, true)", vec.Name, ms, ok, vec.MS)
		}
	}
}

func TestULIDParseVectors(t *testing.T) {
	for _, vec := range loadULIDVectors(t).ParseVectors {
		ms, ok := ulidTimestamp(vec.Text)
		if ok != vec.Valid {
			t.Errorf("%s: parsed %q as valid=%v, want %v", vec.Name, vec.Text, ok, vec.Valid)
			continue
		}
		if vec.Valid && ms != vec.MS {
			t.Errorf("%s: parsed %q to %d, want %d", vec.Name, vec.Text, ms, vec.MS)
		}
		if ulidValid(vec.Text) != vec.Valid {
			t.Errorf("%s: ulidValid disagrees with ulidTimestamp", vec.Name)
		}
	}
}

func TestULIDMintCarriesTheClockAndIsCanonical(t *testing.T) {
	minted, err := ulidMint(1786594672913)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(minted) != 26 {
		t.Fatalf("minted %q is %d characters, want 26", minted, len(minted))
	}
	ms, ok := ulidTimestamp(minted)
	if !ok || ms != 1786594672913 {
		t.Fatalf("minted %q parsed to (%d, %v)", minted, ms, ok)
	}
}

func TestULIDMintRandomnessDiffersAcrossCalls(t *testing.T) {
	// 80 crypto-random bits: a collision in a small sample is not credible.
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		minted, err := ulidMint(1786594672913)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if seen[minted] {
			t.Fatalf("minted %q twice", minted)
		}
		seen[minted] = true
	}
}
