package app

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strconv"
)

const (
	maxReplyBytes = 16 << 20
	maxReplyItems = 100000
	maxReplyDepth = 64
	maxReplyLine  = 64 << 10
)

var errReplyLimit = errors.New("Redis reply exceeds the safety limit (16 MiB, 100000 elements or 64 nesting levels)")

// Validate RESP lengths before go-redis sees a header and allocates its body.
// Bodies are streamed, so this does not buffer a second copy of the reply.
type replyReader struct {
	r      *bufio.Reader
	header []byte
	bulk   int64
	stack  []int64
	bytes  int64
	items  int64
	err    error
}

type replyConn struct {
	net.Conn
	r *replyReader
}

func limitReplies(conn net.Conn) net.Conn {
	return &replyConn{Conn: conn, r: &replyReader{r: bufio.NewReader(conn)}}
}

func (c *replyConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (r *replyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	if len(r.header) > 0 {
		n := copy(p, r.header)
		r.header = r.header[n:]
		return n, nil
	}
	if r.bulk > 0 {
		n, err := r.r.Read(p[:min(int64(len(p)), r.bulk)])
		r.bulk -= int64(n)
		r.err = err
		return n, err
	}
	if err := r.next(); err != nil {
		r.err = err
		return 0, err
	}
	return r.Read(p)
}

func (r *replyReader) next() error {
	for len(r.stack) > 0 && r.stack[len(r.stack)-1] == 0 {
		r.stack = r.stack[:len(r.stack)-1]
	}
	if len(r.stack) == 0 {
		r.stack = append(r.stack, 1)
		r.bytes, r.items = 0, 1
	}
	line, err := boundedLine(r.r, maxReplyLine)
	if err != nil {
		return err
	}
	if len(line) < 3 || line[len(line)-2] != '\r' {
		return errors.New("invalid RESP header")
	}
	r.bytes += int64(len(line))
	r.stack[len(r.stack)-1]--
	switch line[0] {
	case '+', '-', ':', '_', '#', ',', '(':
	case '$', '!', '=', '*', '~', '>', '%', '|':
		n, err := strconv.ParseInt(string(line[1:len(line)-2]), 10, 64)
		if err != nil || n < -1 {
			return errors.New("invalid RESP length")
		}
		if n >= 0 {
			switch line[0] {
			case '$', '!', '=':
				if n > maxReplyBytes-r.bytes-2 {
					return errReplyLimit
				}
				r.bulk = n + 2
				r.bytes += r.bulk
			default:
				if n > maxReplyItems {
					return errReplyLimit
				}
				if line[0] == '%' || line[0] == '|' {
					n *= 2
				}
				// Attributes prefix the next value, including chained blocks.
				if line[0] == '|' {
					n++
				}
				r.items += n
				if r.items > maxReplyItems || len(r.stack) >= maxReplyDepth {
					return errReplyLimit
				}
				if n > 0 {
					r.stack = append(r.stack, n)
				}
			}
		}
	default:
		return errors.New("unsupported RESP reply type")
	}
	if r.bytes > maxReplyBytes {
		return errReplyLimit
	}
	r.header = line
	return nil
}

func boundedLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > limit {
			return nil, errReplyLimit
		}
		line = append(line, part...)
		if err != bufio.ErrBufferFull {
			if err == io.EOF && len(line) > 0 {
				err = io.ErrUnexpectedEOF
			}
			return line, err
		}
	}
}
