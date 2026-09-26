package strictcli

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// outputMember accumulates the envelope's `output` member (contract §19.2's
// box): the bytes ctx.Out would have written in human mode plus every captured
// child's stdout (§19.11), concatenated in the order the framework received
// them. It is written from the handler's goroutine and from child-capture
// goroutines, so every access is locked.
type outputMember struct {
	mu      sync.Mutex
	text    strings.Builder
	written bool
	// captures counts child-stdout readers still draining; the exit step
	// waits for them before it reads the member.
	captures sync.WaitGroup
}

// appendText appends text, which is already a string and is carried unchanged.
func (o *outputMember) appendText(text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.text.WriteString(text)
	o.written = true
}

// value returns the member's JSON value: nil (null) when nothing was written.
// It waits for every child capture to drain first.
func (o *outputMember) value() *string {
	o.captures.Wait()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.written {
		return nil
	}
	s := o.text.String()
	return &s
}

// childCapture is an io.Writer that decodes one child's stdout into the
// member as it arrives. A multi-byte sequence split across two writes is held
// back until the next write or close completes it.
type childCapture struct {
	out     *outputMember
	pending []byte
}

func (c *childCapture) Write(p []byte) (int, error) {
	data := append(c.pending, p...)
	text, rest := decodeUTF8Replacing(data, false)
	c.pending = append([]byte(nil), rest...)
	if text != "" || len(p) > 0 {
		c.out.appendText(text)
	}
	return len(p), nil
}

// close flushes an incomplete trailing sequence as one U+FFFD.
func (c *childCapture) close() {
	if len(c.pending) == 0 {
		return
	}
	text, _ := decodeUTF8Replacing(c.pending, true)
	c.pending = nil
	c.out.appendText(text)
}

// decodeUTF8Replacing decodes b as UTF-8 by the WHATWG decoder's rule: each
// maximal subpart of an ill-formed sequence becomes one U+FFFD (which is not
// what strings.ToValidUTF8 or a per-byte utf8.DecodeRune loop produce). When
// final is false, an incomplete sequence at the end is returned undecoded as
// rest so a later chunk can complete it.
func decodeUTF8Replacing(b []byte, final bool) (string, []byte) {
	var sb strings.Builder
	i := 0
	for i < len(b) {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		need, lower, upper := 0, byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lower = 2, 0xA0
		case c >= 0xE1 && c <= 0xEC, c == 0xEE, c == 0xEF:
			need = 2
		case c == 0xED:
			need, upper = 2, 0x9F
		case c == 0xF0:
			need, lower = 3, 0x90
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		case c == 0xF4:
			need, upper = 3, 0x8F
		default:
			sb.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		complete := true
		for seen := 0; seen < need; seen++ {
			if j >= len(b) {
				if !final {
					return sb.String(), b[i:]
				}
				complete = false
				break
			}
			if b[j] < lower || b[j] > upper {
				complete = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if !complete {
			// The maximal subpart b[i:j] becomes one replacement; the byte
			// that broke it (if any) starts the next sequence.
			sb.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		sb.Write(b[i:j])
		i = j
	}
	return sb.String(), nil
}
