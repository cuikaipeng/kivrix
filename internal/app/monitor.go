package app

// MONITOR needs its own raw connection: go-redis has no streaming command
// API, and taking a pooled connection would poison the pool.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

const maxMonitorLine = 1 << 20

// monitorAddr is the node MONITOR connects to. Sentinel asks for the
// current master; a cluster follows its first seed node.
func (s *Store) monitorAddr(ctx context.Context) (string, error) {
	if s.kind == "sentinel" {
		return s.sentinelMaster(ctx)
	}
	if s.kind == "cluster" && len(s.cfg.ClusterAddrs) > 0 {
		return s.cfg.ClusterAddrs[0], nil
	}
	if s.cfg.Addr != "" {
		return s.cfg.Addr, nil
	}
	if len(s.cfg.ClusterAddrs) > 0 {
		return s.cfg.ClusterAddrs[0], nil
	}
	return "", errors.New("no server address")
}

// sentinelMaster asks the sentinels which node is the current master.
func (s *Store) sentinelMaster(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.connectionTimeout())
	defer cancel()
	var lastErr error
	for _, addr := range s.cfg.SentinelAddrs {
		cl := redis.NewClient(&redis.Options{
			Addr:     addr,
			Username: s.cfg.SentinelUsername,
			Password: s.cfg.SentinelPassword,
			Dialer:   s.dial,
		})
		res, err := cl.Do(ctx, "SENTINEL", "get-master-addr-by-name", s.cfg.MasterName).Result()
		cl.Close()
		if err != nil {
			lastErr = err
			continue
		}
		arr, ok := res.([]any)
		if !ok || len(arr) < 2 {
			lastErr = fmt.Errorf("sentinel %s: unexpected master reply %v", addr, res)
			continue
		}
		host, port := fmt.Sprint(arr[0]), fmt.Sprint(arr[1])
		if host == "" || port == "" {
			lastErr = fmt.Errorf("sentinel %s: empty master reply", addr)
			continue
		}
		return net.JoinHostPort(host, port), nil
	}
	if lastErr == nil {
		lastErr = errors.New("sentinel: no master found")
	}
	return "", lastErr
}

// Monitor streams the server's MONITOR lines until ctx is cancelled or the
// server closes the connection.
func (s *Store) Monitor(ctx context.Context, onLine func(string)) error {
	if s.dial == nil {
		return errNotConnected
	}
	addr, err := s.monitorAddr(ctx)
	if err != nil {
		return fmt.Errorf("monitor: %w", err)
	}
	network := s.cfg.Network
	if network == "" {
		network = "tcp"
	}
	conn, err := s.dial(ctx, network, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	r := bufio.NewReaderSize(conn, 64*1024)
	if s.cfg.Username != "" || s.cfg.Password != "" {
		args := []string{"AUTH"}
		if s.cfg.Username != "" {
			args = append(args, s.cfg.Username, s.cfg.Password)
		} else {
			args = append(args, s.cfg.Password)
		}
		if err := writeCommand(conn, args...); err != nil {
			return err
		}
		reply, err := readReply(r)
		if err != nil {
			return err
		}
		if reply != "OK" {
			return fmt.Errorf("monitor: AUTH replied %v", reply)
		}
	}
	if err := writeCommand(conn, "MONITOR"); err != nil {
		return err
	}
	reply, err := readReply(r)
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("monitor: MONITOR replied %v", reply)
	}
	for {
		reply, err := readReply(r)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		line, ok := reply.(string)
		if !ok {
			return fmt.Errorf("monitor: unexpected reply %T", reply)
		}
		onLine(line)
	}
}

func writeCommand(w io.Writer, args ...string) error {
	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintf(bw, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		if _, err := fmt.Fprintf(bw, "$%d\r\n%s\r\n", len(a), a); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// readReply reads one RESP2 reply. Monitor lines are bulk strings.
func readReply(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" {
		return nil, errors.New("empty RESP reply")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, errors.New(line[1:])
	case ':':
		n, err := strconv.ParseInt(line[1:], 10, 64)
		return n, err
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		if n > maxMonitorLine {
			return nil, fmt.Errorf("RESP bulk reply is too large (%d bytes)", n)
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, err := readReply(r)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown RESP reply %q", line)
	}
}
