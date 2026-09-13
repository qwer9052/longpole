// Package hashlog parses the output of GODEBUG=gocachehash=1, which prints the
// complete ordered input stream of every build-cache hash to stderr.
//
// Diffing two runs' input streams is what turns "this package rebuilt" into
// "this package rebuilt because a.go changed". The GODEBUG is undocumented and
// carries no compatibility guarantee, so malformed lines degrade quietly rather
// than breaking the build report.
//
// The volume is large, so lines are processed as they arrive and raw stderr is
// never buffered.
package hashlog

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Block is one hash's ordered inputs and its final digest. Name is the text
// inside the brackets, for example "build example.com/lab/a".
type Block struct {
	Name   string
	Inputs []string
	Digest string
}

// Collector accumulates hash blocks as lines arrive.
type Collector struct {
	blocks    map[string]*Block
	ambiguous map[string]struct{}
}

// New returns an empty Collector.
func New() *Collector {
	return &Collector{
		blocks:    make(map[string]*Block),
		ambiguous: make(map[string]struct{}),
	}
}

var (
	prefixHash   = []byte("HASH[")
	prefixSubkey = []byte("HASH subkey ")
	prefixFile   = []byte("HASH ")
)

// IsHashLine reports whether line is output caused by gocachehash. It must be
// deliberately narrow because callers suppress matching lines from stderr.
func IsHashLine(line []byte) bool {
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return false
	}
	if bytes.HasPrefix(line, prefixHash) || bytes.HasPrefix(line, prefixSubkey) {
		return true
	}
	if !bytes.HasPrefix(line, prefixFile) {
		return false
	}

	i := bytes.LastIndex(line, []byte(": "))
	return i > len(prefixFile) && isHex64(line[i+2:])
}

func isHex64(value []byte) bool {
	if len(value) != 64 {
		return false
	}
	for _, b := range value {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

// Line consumes one stderr line. Lines outside the HASH[name] form are
// ignored, so callers may safely pass every line from the child process.
func (c *Collector) Line(line []byte) {
	line = bytes.TrimRight(line, "\r\n")
	if !bytes.HasPrefix(line, prefixHash) {
		return
	}

	end := bytes.IndexByte(line, ']')
	if end < len(prefixHash) {
		return
	}
	name := string(line[len(prefixHash):end])

	rest := line[end+1:]
	if len(rest) == 0 {
		c.open(name)
		return
	}
	if !bytes.HasPrefix(rest, []byte(": ")) {
		return
	}
	if _, ambiguous := c.ambiguous[name]; ambiguous {
		return
	}
	block := c.block(name)
	value := string(rest[2:])
	if strings.HasPrefix(value, `"`) {
		input, err := strconv.Unquote(value)
		if err != nil {
			// Preserve an unrecognised value so a later diff can still identify
			// its position without making analysis fail.
			input = value
		}
		block.Inputs = append(block.Inputs, input)
		return
	}
	if isHex64(rest[2:]) {
		block.Digest = value
	}
}

func (c *Collector) open(name string) {
	if _, ambiguous := c.ambiguous[name]; ambiguous {
		return
	}
	if _, exists := c.blocks[name]; exists {
		// The same key can represent distinct normal and test hashes. There is
		// no stable instance identifier in gocachehash output, so returning a
		// merged block would invent a false cause. Omit it instead.
		delete(c.blocks, name)
		c.ambiguous[name] = struct{}{}
		return
	}
	c.blocks[name] = &Block{Name: name}
}

func (c *Collector) block(name string) *Block {
	if block, ok := c.blocks[name]; ok {
		return block
	}
	block := &Block{Name: name}
	c.blocks[name] = block
	return block
}

// Blocks returns all collected blocks, keyed by name.
func (c *Collector) Blocks() map[string]Block {
	blocks := make(map[string]Block, len(c.blocks))
	for name, block := range c.blocks {
		if _, ambiguous := c.ambiguous[name]; ambiguous {
			continue
		}
		blocks[name] = *block
	}
	return blocks
}

// ActionID converts a full hex digest into the identifier stored in the action
// graph. The go command takes the first 15 bytes and uses URL-safe base64.
func ActionID(digest string) (string, error) {
	raw, err := hex.DecodeString(digest)
	if err != nil {
		return "", fmt.Errorf("decode digest: %w", err)
	}
	if len(raw) < 15 {
		return "", fmt.Errorf("digest is %d bytes, need at least 15", len(raw))
	}
	return base64.URLEncoding.EncodeToString(raw[:15]), nil
}
