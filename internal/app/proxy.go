package app

// HTTP CONNECT and SOCKS5 dialers for Redis connections.

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// baseDialer returns a TCP dialer that goes through the configured proxy.
func baseDialer(p ProxyConfig) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	var d net.Dialer
	direct := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return d.DialContext(ctx, network, addr)
	}
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "", "none":
		return direct, nil
	case "http":
		if p.Addr == "" {
			return nil, errors.New("proxy: an HTTP proxy address is required")
		}
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialHTTPProxy(ctx, p, addr)
		}, nil
	case "socks5":
		if p.Addr == "" {
			return nil, errors.New("proxy: a SOCKS5 address is required")
		}
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialSOCKS5(ctx, p, addr)
		}, nil
	default:
		return nil, fmt.Errorf("proxy: unknown type %q", p.Type)
	}
}

func proxyConn(ctx context.Context, p ProxyConfig) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", p.Addr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	return conn, nil
}

func dialHTTPProxy(ctx context.Context, p ProxyConfig, target string) (net.Conn, error) {
	conn, err := proxyConn(ctx, p)
	if err != nil {
		return nil, err
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if p.Username != "" || p.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(p.Username + ":" + p.Password))
		req += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	req += "Proxy-Connection: Keep-Alive\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy: CONNECT replied %s", resp.Status)
	}
	conn.SetDeadline(time.Time{})
	return &bufferedConn{Conn: conn, r: br}, nil
}

// bufferedConn preserves bytes the HTTP response reader buffered after the
// proxy headers.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func dialSOCKS5(ctx context.Context, p ProxyConfig, target string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("socks5: target %q: %w", target, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("socks5: bad port %q", portText)
	}
	conn, err := proxyConn(ctx, p)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		conn.Close()
		return nil, err
	}

	methods := []byte{0}
	if p.Username != "" || p.Password != "" {
		methods = append(methods, 2)
	}
	if _, err := conn.Write([]byte{5, byte(len(methods))}); err != nil {
		return fail(err)
	}
	if _, err := conn.Write(methods); err != nil {
		return fail(err)
	}
	var choice [2]byte
	if _, err := io.ReadFull(conn, choice[:]); err != nil {
		return fail(err)
	}
	if choice[0] != 5 {
		return fail(fmt.Errorf("socks5: bad version %d", choice[0]))
	}
	switch choice[1] {
	case 0:
	case 2:
		if len(p.Username) > 255 || len(p.Password) > 255 {
			return fail(errors.New("socks5: username or password is too long"))
		}
		auth := []byte{1, byte(len(p.Username))}
		auth = append(auth, p.Username...)
		auth = append(auth, byte(len(p.Password)))
		auth = append(auth, p.Password...)
		if _, err := conn.Write(auth); err != nil {
			return fail(err)
		}
		var status [2]byte
		if _, err := io.ReadFull(conn, status[:]); err != nil {
			return fail(err)
		}
		if status[1] != 0 {
			return fail(errors.New("socks5: authentication failed"))
		}
	case 0xff:
		return fail(errors.New("socks5: no acceptable authentication method"))
	default:
		return fail(fmt.Errorf("socks5: unsupported auth method %d", choice[1]))
	}

	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 1)
			req = append(req, v4...)
		} else {
			req = append(req, 4)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fail(errors.New("socks5: host name is too long"))
		}
		req = append(req, 3, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return fail(err)
	}
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return fail(err)
	}
	if head[0] != 5 {
		return fail(fmt.Errorf("socks5: bad version %d", head[0]))
	}
	if head[1] != 0 {
		return fail(fmt.Errorf("socks5: connect failed with code %d", head[1]))
	}
	switch head[3] {
	case 1:
		var b [4]byte
		_, err = io.ReadFull(conn, b[:])
	case 3:
		var n [1]byte
		if _, err = io.ReadFull(conn, n[:]); err == nil {
			b := make([]byte, n[0])
			_, err = io.ReadFull(conn, b)
		}
	case 4:
		var b [16]byte
		_, err = io.ReadFull(conn, b[:])
	default:
		return fail(fmt.Errorf("socks5: bad address type %d", head[3]))
	}
	if err != nil {
		return fail(err)
	}
	var tail [2]byte
	if _, err := io.ReadFull(conn, tail[:]); err != nil {
		return fail(err)
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}
