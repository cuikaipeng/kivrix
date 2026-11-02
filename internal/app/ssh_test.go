package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestLoadSSHSignerPassphrase(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSSHSigner(path, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSSHSigner(path, "wrong"); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	if _, err := loadSSHSigner(path, ""); err == nil {
		t.Fatal("encrypted key without passphrase accepted")
	}
}

func TestSSHAgentMissingSocket(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("base dialer should not run")
	}
	_, err := dialSSH(context.Background(), ConnConfig{SSH: SSHConfig{Enabled: true, Addr: "127.0.0.1:1", Agent: true, Insecure: true}}, base)
	if err == nil || !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Fatalf("error %v", err)
	}
}

func sshTestServer(t *testing.T, acceptPassword, acceptKey bool) string {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if acceptPassword && string(pass) == "pw" {
				return nil, nil
			}
			return nil, errors.New("bad password")
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if acceptKey {
				return nil, nil
			}
			return nil, errors.New("bad key")
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
			if err != nil {
				conn.Close()
				continue
			}
			go ssh.DiscardRequests(reqs)
			go func() {
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "test")
				}
			}()
			_ = sc
		}
	}()
	return ln.Addr().String()
}

func TestSSHDialPasswordAndAgent(t *testing.T) {
	addr := sshTestServer(t, true, true)
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, addr)
	}
	cl, err := dialSSH(context.Background(), ConnConfig{SSH: SSHConfig{Addr: addr, User: "u", Insecure: true}, SSHPassword: "pw"}, base)
	if err != nil {
		t.Fatal(err)
	}
	cl.Close()

	_, keyPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(keyPriv, "test")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	cl, err = dialSSH(context.Background(), ConnConfig{SSH: SSHConfig{Addr: addr, User: "u", KeyPath: keyPath, Insecure: true}}, base)
	if err != nil {
		t.Fatal(err)
	}
	cl.Close()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			agent.ServeAgent(keyring, conn)
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
	cl, err = dialSSH(context.Background(), ConnConfig{SSH: SSHConfig{Addr: addr, User: "u", Agent: true, Insecure: true}}, base)
	if err != nil {
		t.Fatal(err)
	}
	cl.Close()
}

func TestSSHTunnelDial(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(c)
		}
	}()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
		if string(pass) == "pw" {
			return nil, nil
		}
		return nil, errors.New("bad password")
	}}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "direct-tcpip" {
						ch.Reject(ssh.UnknownChannelType, "only direct-tcpip")
						continue
					}
					var req struct {
						Host       string
						Port       uint32
						OriginHost string
						OriginPort uint32
					}
					if err := ssh.Unmarshal(ch.ExtraData(), &req); err != nil {
						ch.Reject(ssh.Prohibited, "bad request")
						continue
					}
					up, err := net.Dial("tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
					if err != nil {
						ch.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					channel, requests, err := ch.Accept()
					if err != nil {
						up.Close()
						continue
					}
					go ssh.DiscardRequests(requests)
					go io.Copy(channel, up)
					go io.Copy(up, channel)
				}
				_ = sc
			}(c)
		}
	}()
	cl, err := dialSSH(context.Background(), ConnConfig{SSH: SSHConfig{Addr: ln.Addr().String(), User: "u", Insecure: true}, SSHPassword: "pw"},
		func(ctx context.Context, network, addr string) (net.Conn, error) { return net.Dial(network, addr) })
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	dial := tunnelDialer(ConnConfig{}, func(ctx context.Context, network, addr string) (net.Conn, error) {
		return cl.Dial(network, addr)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("echo %q", buf)
	}
}
