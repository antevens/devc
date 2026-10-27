package clipboard

import (
	"bytes"
	"encoding/base64"
	"io"
)

const (
	esc = 0x1b
	bel = 0x07
	// maxPayload is the longest sequence body kept. Longer sequences are
	// removed without a copy.
	maxPayload = 8 << 20
	// keepCapacity is the largest buffer kept for the next sequence.
	keepCapacity = 64 << 10
)

// introducer starts an OSC 52 clipboard sequence.
var introducer = []byte("\x1b]52;")

// Filter passes terminal output through to out, removing OSC 52 sequences.
// The text of clipboard writes is passed to onCopy, clipboard reads get no
// answer. Sequences may be split across writes.
type Filter struct {
	out    io.Writer
	onCopy func([]byte)
	// held is a partial introducer, or the body of the sequence being read.
	held []byte
	// inSequence is set after a complete introducer.
	inSequence bool
	// sawESC is set when the last byte of the sequence was ESC, which may
	// start the string terminator.
	sawESC bool
	// tooLong is set when the sequence body exceeds maxPayload.
	tooLong bool
	// buf collects the output of one Write.
	buf []byte
}

// NewFilter returns a filter writing to out and passing copied text to
// onCopy.
func NewFilter(out io.Writer, onCopy func([]byte)) *Filter {
	return &Filter{out: out, onCopy: onCopy}
}

// Write filters p. Bytes that may start an OSC 52 sequence are held until a
// later write shows whether they do.
func (f *Filter) Write(p []byte) (int, error) {
	n := len(p)
	f.buf = f.buf[:0]
	for len(p) > 0 {
		if !f.inSequence && len(f.held) == 0 {
			i := bytes.IndexByte(p, esc)
			if i < 0 {
				f.buf = append(f.buf, p...)
				break
			}
			f.buf = append(f.buf, p[:i]...)
			p = p[i:]
		}
		f.step(p[0])
		p = p[1:]
	}
	if len(f.buf) > 0 {
		if _, err := f.out.Write(f.buf); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// Flush writes a held partial introducer. An unterminated sequence is
// dropped.
func (f *Filter) Flush() error {
	if f.inSequence || len(f.held) == 0 {
		return nil
	}
	_, err := f.out.Write(f.held)
	f.held = f.held[:0]
	return err
}

func (f *Filter) step(b byte) {
	if f.inSequence {
		f.sequence(b)
		return
	}
	if b == introducer[len(f.held)] {
		f.held = append(f.held, b)
		if len(f.held) == len(introducer) {
			f.inSequence = true
			f.held = f.held[:0]
		}
		return
	}
	f.buf = append(f.buf, f.held...)
	f.held = f.held[:0]
	if b == esc {
		f.held = append(f.held, b)
		return
	}
	f.buf = append(f.buf, b)
}

// sequence reads a byte of the sequence body.
func (f *Filter) sequence(b byte) {
	if f.sawESC {
		f.sawESC = false
		if b == '\\' {
			f.finish()
			return
		}
		// ESC without '\' ends the sequence unterminated. It is dropped and
		// the ESC starts new output.
		f.reset()
		f.step(esc)
		f.step(b)
		return
	}
	switch {
	case b == bel:
		f.finish()
	case b == esc:
		f.sawESC = true
	case f.tooLong:
	case len(f.held) == maxPayload:
		f.tooLong = true
		f.held = nil
	default:
		f.held = append(f.held, b)
	}
}

// finish passes the text of a clipboard write to onCopy and ends the
// sequence. The body is the selections, ';' and the base64 text, or '?' for
// a read.
func (f *Filter) finish() {
	if _, encoded, ok := bytes.Cut(f.held, []byte(";")); ok && !f.tooLong && string(encoded) != "?" {
		if data, err := decode(encoded); err == nil && len(data) > 0 {
			f.onCopy(data)
		}
	}
	f.reset()
}

func (f *Filter) reset() {
	f.inSequence, f.sawESC, f.tooLong = false, false, false
	if cap(f.held) > keepCapacity {
		f.held = nil
	}
	f.held = f.held[:0]
}

// decode decodes standard base64 with or without padding.
func decode(encoded []byte) ([]byte, error) {
	encoded = bytes.TrimRight(encoded, "=")
	data := make([]byte, base64.RawStdEncoding.DecodedLen(len(encoded)))
	n, err := base64.RawStdEncoding.Decode(data, encoded)
	return data[:n], err
}
