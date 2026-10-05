package strictcli

// BodyFile and BodyFileRange: an HTTP request body streamed from a file.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// bodyServer records the body and Content-Length of every request it gets.
func bodyServer(t *testing.T) (*httptest.Server, *[]string, *[]int64) {
	var bodies []string
	var lengths []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		lengths = append(lengths, r.ContentLength)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies, &lengths
}

func writeBodyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBodyFileSendsTheWholeFileWithItsLength(t *testing.T) {
	srv, bodies, lengths := bodyServer(t)
	path := writeBodyFile(t, "0123456789")
	var err error
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().HTTP("PUT", srv.URL, BodyFile(path))
		return Exit(0)
	})
	app.Test([]string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || (*bodies)[0] != "0123456789" || (*lengths)[0] != 10 {
		t.Fatalf("bodies %q lengths %v", *bodies, *lengths)
	}
}

func TestBodyFileRangeSendsOnlyTheRange(t *testing.T) {
	srv, bodies, lengths := bodyServer(t)
	path := writeBodyFile(t, "0123456789")
	var err error
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().HTTP("PUT", srv.URL, BodyFileRange(path, 3, 4))
		return Exit(0)
	})
	app.Test([]string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || (*bodies)[0] != "3456" || (*lengths)[0] != 4 {
		t.Fatalf("bodies %q lengths %v", *bodies, *lengths)
	}
}

// rewritingTransport reads the first half of a request body, overwrites the
// file's second half, and then reads the rest: a body read into memory before
// the request was sent still carries the old bytes, a streamed one the new.
type rewritingTransport struct {
	path string
	half int
	got  atomic.Value
}

func (rt *rewritingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	first := make([]byte, rt.half)
	if _, err := io.ReadFull(r.Body, first); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(rt.path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strings.Repeat("B", rt.half)), int64(rt.half)); err != nil {
		return nil, err
	}
	f.Close()
	rest, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	rt.got.Store(string(first) + string(rest))
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: r}, nil
}

func TestBodyFileIsStreamedFromDiskNotReadIntoMemory(t *testing.T) {
	const half = 1 << 20
	path := writeBodyFile(t, strings.Repeat("A", 2*half))
	rt := &rewritingTransport{path: path, half: half}
	app := NewApp("app", "1.0.0", "effects fixture", WithHTTPClient(&http.Client{Transport: rt}))
	var err error
	app.Command("go", "run the fixture handler", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		_, err = ctx.Effects().HTTP("PUT", "https://upload.test/part", BodyFile(path))
		return Exit(0)
	}, WithEffect(EffectMutating))
	app.Test([]string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := rt.got.Load().(string)
	if got != strings.Repeat("A", half)+strings.Repeat("B", half) {
		t.Fatalf("the body was not read from disk while it was sent: %d bytes, %d of them B", len(got), strings.Count(got, "B"))
	}
}

func TestBodyFileRendersItsPathAndByteCountInADryRun(t *testing.T) {
	var hits atomic.Int64
	srv := okServer(t, &hits)
	path := writeBodyFile(t, "0123456789")
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().HTTP("PUT", srv.URL+"/a", BodyFile(path))
		ctx.Effects().HTTP("PUT", srv.URL+"/b", BodyFileRange(path, 2, 5))
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	for _, want := range []string{
		"1. net: PUT " + srv.URL + "/a (body: 10 bytes from " + path + ")",
		"2. net: PUT " + srv.URL + "/b (body: 5 bytes from " + path + " at offset 2)",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("got %q, want a line %q", r.Stdout, want)
		}
	}
	log := app.EffectLog()
	if log[0]["body_bytes"] != int64(10) || log[0]["body_file"] != path || log[1]["body_offset"] != int64(2) {
		blob, _ := json.Marshal(log)
		t.Fatalf("records %s", blob)
	}
	if hits.Load() != 0 {
		t.Fatalf("a dry run sent %d requests", hits.Load())
	}
}

func TestBodyFileRefusals(t *testing.T) {
	path := writeBodyFile(t, "0123456789")
	cases := []struct {
		opts []EffectOption
		want string
	}{
		{[]EffectOption{Body([]byte("x")), BodyFile(path)}, "option 'body_file' cannot be combined with 'body'"},
		{[]EffectOption{BodyFile(path), BodyFile(path)}, "option 'body_file' is given more than once"},
		{[]EffectOption{BodyFile(filepath.Join(t.TempDir(), "missing"))}, "option 'body_file': "},
		{[]EffectOption{BodyFile(t.TempDir())}, "is not a regular file"},
		{[]EffectOption{BodyFileRange(path, 8, 4)}, "bytes 8+4 are outside the 10-byte file"},
		{[]EffectOption{BodyFileRange(path, -1, 4)}, "bytes -1+4 are outside the 10-byte file"},
		{[]EffectOption{BodyFileRange(path, 0, 0)}, "the length must be positive, got 0"},
	}
	for _, c := range cases {
		var err error
		app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
			_, err = ctx.Effects().HTTP("PUT", "https://upload.test/x", c.opts...)
			return Exit(0)
		})
		app.Test([]string{"--dry-run", "go"})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want an error containing %q, got %v", c.want, err)
		}
	}
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		_, err := ctx.Effects().Run([]interface{}{"true"}, BodyFile(path))
		if err == nil || !strings.Contains(err.Error(), "does not accept option 'body_file'") {
			t.Errorf("Run took a body file: %v", err)
		}
		return Exit(0)
	})
	app.Test([]string{"--dry-run", "go"})
}
