package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProxyDialerValidation(t *testing.T) {
	if _, err := baseDialer(ProxyConfig{Type: "http"}); err == nil {
		t.Fatal("missing HTTP proxy address")
	}
	if _, err := baseDialer(ProxyConfig{Type: "socks5"}); err == nil {
		t.Fatal("missing SOCKS5 proxy address")
	}
	if _, err := baseDialer(ProxyConfig{Type: "ftp", Addr: "p:1"}); err == nil {
		t.Fatal("unknown proxy type")
	}
	if _, err := baseDialer(ProxyConfig{}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPProxyDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		line, err := br.ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		if !strings.HasPrefix(line, "CONNECT example.com:6379 ") {
			done <- io.ErrUnexpectedEOF
			return
		}
		auth := ""
		for {
			h, err := br.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			if h == "\r\n" {
				break
			}
			if strings.HasPrefix(strings.ToLower(h), "proxy-authorization:") {
				auth = strings.TrimSpace(h)
			}
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
		if !strings.Contains(auth, want) {
			done <- io.ErrUnexpectedEOF
			return
		}
		// Send the response and tunnel data in one write to exercise the
		// buffered connection wrapper.
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\nbuffered"); err != nil {
			done <- err
			return
		}
		done <- nil
	}()
	dial, err := baseDialer(ProxyConfig{Type: "http", Addr: ln.Addr().String(), Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "example.com:6379")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	buf := make([]byte, len("buffered"))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "buffered" {
		t.Fatalf("payload %q", buf)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5ProxyDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		var greet [2]byte
		if _, err := io.ReadFull(conn, greet[:]); err != nil {
			done <- err
			return
		}
		methods := make([]byte, greet[1])
		if _, err := io.ReadFull(conn, methods); err != nil {
			done <- err
			return
		}
		if !strings.Contains(string(methods), string([]byte{2})) {
			done <- io.ErrUnexpectedEOF
			return
		}
		conn.Write([]byte{5, 2})
		var head [2]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			done <- err
			return
		}
		user := make([]byte, head[1])
		io.ReadFull(conn, user)
		var plen [1]byte
		io.ReadFull(conn, plen[:])
		password := make([]byte, plen[0])
		io.ReadFull(conn, password)
		if string(user) != "u" || string(password) != "p" {
			done <- io.ErrUnexpectedEOF
			return
		}
		conn.Write([]byte{1, 0})
		var req [4]byte
		if _, err := io.ReadFull(conn, req[:]); err != nil {
			done <- err
			return
		}
		if req[3] != 3 {
			done <- io.ErrUnexpectedEOF
			return
		}
		var n [1]byte
		io.ReadFull(conn, n[:])
		host := make([]byte, n[0])
		io.ReadFull(conn, host)
		var port [2]byte
		io.ReadFull(conn, port[:])
		if string(host) != "example.com" || int(port[0])<<8|int(port[1]) != 6379 {
			done <- io.ErrUnexpectedEOF
			return
		}
		conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		conn.Write([]byte("tunnel"))
		done <- nil
	}()
	dial, err := baseDialer(ProxyConfig{Type: "socks5", Addr: ln.Addr().String(), Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "example.com:6379")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	buf := make([]byte, len("tunnel"))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "tunnel" {
		t.Fatalf("payload %q", buf)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
