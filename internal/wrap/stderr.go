package wrap

import (
	"bytes"
	"io"
)

// StderrTee forwards the child's stderr while diverting lines that longpole
// itself caused. Under --explain the GODEBUG we set makes the go command print
// a large volume of HASH lines; the user did not ask to see those, so they are
// consumed rather than forwarded.
//
// This is the only place longpole alters a child's output, and it only removes
// output it caused.
type StderrTee struct {
	Out    io.Writer
	OnLine func([]byte) bool // return true to swallow the line

	buf bytes.Buffer
}

// NewStderrTee returns a tee writing through to out.
func NewStderrTee(out io.Writer, onLine func([]byte) bool) *StderrTee {
	return &StderrTee{Out: out, OnLine: onLine}
}

func (t *StderrTee) Write(p []byte) (int, error) {
	n := len(p)
	t.buf.Write(p)
	for {
		i := bytes.IndexByte(t.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := make([]byte, i+1)
		copy(line, t.buf.Next(i+1))
		if t.OnLine != nil && t.OnLine(line) {
			continue
		}
		if _, err := t.Out.Write(line); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Flush writes any trailing partial line. The go command does not always end
// its output with a newline.
func (t *StderrTee) Flush() error {
	if t.buf.Len() == 0 {
		return nil
	}
	line := t.buf.Bytes()
	if t.OnLine != nil && t.OnLine(line) {
		t.buf.Reset()
		return nil
	}
	_, err := t.Out.Write(line)
	t.buf.Reset()
	return err
}
