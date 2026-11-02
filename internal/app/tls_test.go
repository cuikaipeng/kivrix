package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeCert(t *testing.T, dns string) (certPEM, keyPEM []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: dns},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{dns},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTLSConfigFor(t *testing.T) {
	dir := t.TempDir()
	serverCert, _ := makeCert(t, "localhost")
	clientCert, clientKey := makeCert(t, "client")
	caPath := filepath.Join(dir, "ca.pem")
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeFile(t, caPath, serverCert)
	writeFile(t, certPath, clientCert)
	writeFile(t, keyPath, clientKey)

	tc, err := tlsConfigFor(ConnConfig{UseTLS: true, TLS: TLSConfig{CA: caPath, Cert: certPath, Key: keyPath, ServerName: "localhost"}}, "127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}
	if tc.ServerName != "localhost" || tc.RootCAs == nil || len(tc.Certificates) != 1 {
		t.Fatalf("config %+v", tc)
	}
	if _, err := tlsConfigFor(ConnConfig{UseTLS: true, TLS: TLSConfig{CA: filepath.Join(dir, "missing")}}, "a:1"); err == nil {
		t.Fatal("missing CA accepted")
	}
	if _, err := tlsConfigFor(ConnConfig{UseTLS: true, TLS: TLSConfig{Cert: certPath}}, "a:1"); err == nil {
		t.Fatal("certificate without key accepted")
	}
	if _, err := tlsConfigFor(ConnConfig{UseTLS: true, TLS: TLSConfig{Insecure: true}}, "a:1"); err != nil {
		t.Fatal(err)
	}
}

func TestTLSDialWithCAAndClientCert(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey := makeCert(t, "localhost")
	clientCert, clientKey := makeCert(t, "client")
	certPair, err := tls.X509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{certPair},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
	})
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
		buf := make([]byte, 1)
		if _, err := conn.Read(buf); err != nil {
			done <- err
			return
		}
		_, err = conn.Write([]byte("pong"))
		done <- err
	}()
	caPath := filepath.Join(dir, "ca.pem")
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeFile(t, caPath, serverCert)
	writeFile(t, certPath, clientCert)
	writeFile(t, keyPath, clientKey)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	dial := tunnelDialer(ConnConfig{UseTLS: true, TLS: TLSConfig{CA: caPath, Cert: certPath, Key: keyPath, ServerName: "localhost"}},
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ln.Addr().String())
		})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "localhost:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "pong" {
		t.Fatalf("reply %q", buf)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
