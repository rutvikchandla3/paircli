package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
)

// collector is the writer a source file is streamed through. It hashes
// everything, remembers the hash as of the last newline seen (the only
// positions a cursor may take), and keeps the bytes from `from` on, up to
// limit; past that it stops keeping them and reports overflow.
type collector struct {
	h        hash.Hash
	from     int64
	limit    int64
	pos      int64
	buf      []byte
	overflow bool
	lineEnd  int64
	lineSum  string
}

func newCollector(from, limit int64) *collector {
	return &collector{h: sha256.New(), from: from, limit: limit}
}

func (c *collector) Write(p []byte) (int, error) {
	if !c.overflow {
		q := p
		if c.pos < c.from {
			skip := c.from - c.pos
			if skip >= int64(len(q)) {
				q = nil
			} else {
				q = q[skip:]
			}
		}
		if int64(len(c.buf))+int64(len(q)) > c.limit {
			c.overflow, c.buf = true, nil
		} else {
			c.buf = append(c.buf, q...)
		}
	}
	if i := bytes.LastIndexByte(p, '\n'); i >= 0 {
		c.h.Write(p[:i+1])
		c.lineSum = hex.EncodeToString(c.h.Sum(nil))
		c.lineEnd = c.pos + int64(i) + 1
		c.h.Write(p[i+1:])
	} else {
		c.h.Write(p)
	}
	c.pos += int64(len(p))
	return len(p), nil
}

// readResult is what streaming one source file produced.
type readResult struct {
	start, end int64  // the captured range [start, end), end just after a newline
	sum        string // SHA-256 of bytes [0, end), the next cursor's prefix hash
	data       []byte // bytes [start, end); nil when dropped
	dropped    bool   // the range exceeded the budget and was skipped
	reset      bool   // the previous cursor no longer matched the file
}

// readSource streams path, verifying the previous cursor against the file and
// returning the complete lines past it. When cur names the same head as head,
// the range starts where that head's earlier publication began, so a rerun of
// a head repeats what it already uploaded. A cursor that no longer matches
// (the file shrank, was rotated or rewritten) restarts from offset 0. At most
// budget bytes are kept; a larger range is dropped but the cursor still moves
// past it.
func readSource(path string, cur cursor, head string, budget int64) (readResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return readResult{}, err
	}
	defer f.Close()

	var c *collector
	res := readResult{}
	if cur.Offset > 0 {
		from := cur.Offset
		if cur.Head == head && cur.HeadStart >= 0 && cur.HeadStart < cur.Offset {
			from = cur.HeadStart
		}
		c = newCollector(from, budget)
		n, err := io.CopyN(c, f, cur.Offset)
		if err != nil || n != cur.Offset || c.lineEnd != cur.Offset || c.lineSum != cur.PrefixSHA256 {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return readResult{}, err
			}
			c, res.reset = nil, true
		} else {
			res.start = from
		}
	}
	if c == nil {
		c = newCollector(0, budget)
	}
	if _, err := io.Copy(c, f); err != nil {
		return readResult{}, err
	}

	res.end, res.sum = c.lineEnd, c.lineSum
	if c.overflow {
		res.dropped = true
		return res, nil
	}
	if res.end > res.start {
		res.data = c.buf[:res.end-res.start]
	}
	return res, nil
}
