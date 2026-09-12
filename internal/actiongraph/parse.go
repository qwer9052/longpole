// Package actiongraph decodes the JSON that `go build -debug-actiongraph=FILE`
// writes. It performs no interpretation: every derived value lives in
// internal/model. The flag is undocumented and unsupported by the Go team, but
// the record shape has been byte-for-byte identical from Go 1.21 through 1.27.
package actiongraph

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Action mirrors cmd/go/internal/work.actionJSON exactly. Field names and types
// must not be changed to something more convenient; they are the wire format.
//
// Only TimeReady, TimeStart, TimeDone, ActionID, BuildID, Cmd, CmdReal, CmdUser
// and CmdSys reflect what actually happened. Every other field is frozen before
// the build runs, because the go command writes this file twice and only
// populates the struct on the first write.
type Action struct {
	ID         int
	Mode       string
	Package    string
	Deps       []int
	IgnoreFail bool
	Args       []string
	Link       bool
	Objdir     string
	Target     string
	Priority   int
	Failed     bool
	Built      string
	VetxOnly   bool
	NeedVet    bool
	NeedBuild  bool
	ActionID   string
	BuildID    string
	TimeReady  time.Time
	TimeStart  time.Time
	TimeDone   time.Time

	Cmd     []string
	CmdReal time.Duration
	CmdUser time.Duration
	CmdSys  time.Duration
}

// Parse decodes an action graph. It streams rather than reading the whole file
// into memory first, because graphs from large repositories reach tens of
// megabytes.
func Parse(r io.Reader) ([]Action, error) {
	dec := json.NewDecoder(r)

	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("read opening token: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("expected a JSON array, got %v", tok)
	}

	var acts []Action
	for dec.More() {
		var a Action
		if err := dec.Decode(&a); err != nil {
			return nil, fmt.Errorf("decode action %d: %w", len(acts), err)
		}
		acts = append(acts, a)
	}

	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("read closing token: %w", err)
	}
	if tok, err := dec.Token(); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("read after action graph: %w", err)
		}
		return nil, fmt.Errorf("unexpected token after action graph: %v", tok)
	}
	return acts, nil
}

// ParseFile is the convenience wrapper used by the command layer.
func ParseFile(path string) ([]Action, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open action graph %q: %w", path, err)
	}
	defer f.Close()
	return Parse(f)
}
