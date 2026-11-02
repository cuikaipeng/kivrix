package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func testStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	var st Store
	if err := st.Connect(ConnConfig{Addr: mr.Addr()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return &st, mr
}

func TestConnectFails(t *testing.T) {
	var st Store
	if err := st.Connect(ConnConfig{Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("expected an error connecting to a closed port")
	}
}

func TestConnectAuth(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("secret")
	var st Store
	if err := st.Connect(ConnConfig{Addr: mr.Addr(), Password: "wrong"}); err == nil {
		t.Fatal("expected an auth error")
	}
	if err := st.Connect(ConnConfig{Addr: mr.Addr(), Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
}

func TestScanKeys(t *testing.T) {
	st, mr := testStore(t)
	for _, k := range []string{"user:1", "user:2", "sess:a", "sess:b", "plain"} {
		mr.Set(k, "v")
	}
	var got []string
	done, n, err := st.ScanKeys(context.Background(), "", 0, func(keys []string) {
		got = append(got, keys...)
	})
	if err != nil || !done || n != 5 {
		t.Fatalf("done %v n %d err %v", done, n, err)
	}
	if len(got) != 5 {
		t.Fatalf("keys %v", got)
	}

	// Pattern narrows the scan; the limit cuts it short.
	got = nil
	done, n, err = st.ScanKeys(context.Background(), "user:*", 1, func(keys []string) {
		got = append(got, keys...)
	})
	if err != nil || done || n != 1 || len(got) != 1 {
		t.Fatalf("done %v n %d got %v err %v", done, n, got, err)
	}
}

func TestGetValue(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()

	mr.Set("str", `{"a":1}`)
	mr.SetTTL("str", time.Minute)
	v, err := st.GetValue(ctx, "str")
	if err != nil || v.Type != "string" || v.Str != `{"a":1}` {
		t.Fatalf("string value %+v err %v", v, err)
	}
	if v.TTL < 59*time.Second || v.TTL > time.Minute {
		t.Fatalf("ttl %v", v.TTL)
	}

	mr.RPush("list", "a", "b")
	v, err = st.GetValue(ctx, "list")
	if err != nil || v.Type != "list" || len(v.Items) != 2 || v.Items[0] != "a" || v.Size != 2 {
		t.Fatalf("list value %+v err %v", v, err)
	}

	mr.HSet("hash", "f1", "v1", "f2", "v2")
	v, err = st.GetValue(ctx, "hash")
	if err != nil || v.Type != "hash" || len(v.Pairs) != 2 || v.Pairs[0] != [2]string{"f1", "v1"} {
		t.Fatalf("hash value %+v err %v", v, err)
	}

	mr.SAdd("set", "m2", "m1")
	v, err = st.GetValue(ctx, "set")
	if err != nil || v.Type != "set" || len(v.Items) != 2 || v.Items[0] != "m1" {
		t.Fatalf("set value %+v err %v", v, err)
	}

	mr.ZAdd("zset", 1.5, "m1")
	mr.ZAdd("zset", 2, "m2")
	v, err = st.GetValue(ctx, "zset")
	if err != nil || v.Type != "zset" || len(v.Pairs) != 2 || v.Pairs[0] != [2]string{"m1", "1.5"} {
		t.Fatalf("zset value %+v err %v", v, err)
	}

	v, err = st.GetValue(ctx, "missing")
	if err != nil || v.Type != "none" {
		t.Fatalf("missing key %+v err %v", v, err)
	}
}

func TestSetStringKeepsTTL(t *testing.T) {
	st, mr := testStore(t)
	mr.Set("k", "old")
	mr.SetTTL("k", time.Hour)
	if err := st.SetString(context.Background(), "k", "new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := mr.Get("k"); got != "new" {
		t.Fatalf("value %q", got)
	}
	if mr.TTL("k") != time.Hour {
		t.Fatalf("ttl %v", mr.TTL("k"))
	}
}

func TestDeleteKeys(t *testing.T) {
	st, mr := testStore(t)
	mr.Set("a", "1")
	mr.Set("b", "2")
	n, err := st.DeleteKeys(context.Background(), "a", "b", "missing")
	if err != nil || n != 2 {
		t.Fatalf("n %d err %v", n, err)
	}
	if mr.Exists("a") || mr.Exists("b") {
		t.Fatal("keys still exist")
	}
}

func TestScanPage(t *testing.T) {
	st, mr := testStore(t)
	for i := 0; i < 7; i++ {
		mr.Set(fmt.Sprintf("k%d", i), "v")
	}
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		keys, next, err := st.ScanPage(context.Background(), cursor, "*", 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			seen[k] = true
		}
		if next == "" {
			break
		}
		cursor = next
		if pages > 20 {
			t.Fatal("SCAN did not finish")
		}
	}
	if len(seen) != 7 {
		t.Fatalf("saw %d keys: %v", len(seen), seen)
	}
}

func TestSelectDB(t *testing.T) {
	st, mr := testStore(t)
	mr.Set("zero", "0")
	mr.Select(1)
	mr.Set("one", "1")
	mr.Select(0)
	if err := st.SelectDB(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	v, err := st.GetValue(context.Background(), "one")
	if err != nil || v.Str != "1" {
		t.Fatalf("db1 value %+v err %v", v, err)
	}
	if err := st.SelectDB(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	v, err = st.GetValue(context.Background(), "zero")
	if err != nil || v.Str != "0" {
		t.Fatalf("db0 value %+v err %v", v, err)
	}
}

func TestFormatCount(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1500: "1.5k", 2_400_000: "2.4m"}
	for n, want := range cases {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestValuePages(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()

	// A list pages by offset.
	for i := 0; i < 120; i++ {
		mr.RPush("l", fmt.Sprintf("e%03d", i))
	}
	p, err := st.GetValuePage(ctx, "l", 0, 0, 50, "")
	if err != nil || len(p.Items) != 50 || p.NextOffset != 50 || !p.HasMore {
		t.Fatalf("list page %+v err %v", p, err)
	}
	p, err = st.GetValuePage(ctx, "l", 0, p.NextOffset, 50, "")
	if err != nil || len(p.Items) != 50 || p.NextOffset != 100 || !p.HasMore {
		t.Fatalf("list page 2 %+v err %v", p, err)
	}
	p, err = st.GetValuePage(ctx, "l", 0, 15, 3, "*e01*")
	if err != nil || len(p.Items) != 3 || p.Items[0] != "e015" || p.ListIndices[0] != 15 || p.Size != 120 || p.NextOffset != 18 {
		t.Fatalf("list filter %+v %v", p, err)
	}

	// A hash pages by cursor and filters fields.
	for i := 0; i < 40; i++ {
		mr.HSet("h", fmt.Sprintf("f%02d", i), fmt.Sprintf("v%02d", i))
	}
	seen := map[string]bool{}
	var cursor uint64
	for pages := 0; pages < 10; pages++ {
		p, err = st.GetValuePage(ctx, "h", cursor, 0, 15, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, kv := range p.Pairs {
			seen[kv[0]] = true
		}
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 40 {
		t.Fatalf("hash pages saw %d", len(seen))
	}
	p, err = st.GetValuePage(ctx, "h", 0, 0, 10, "f0*")
	if err != nil || len(p.Pairs) != 10 {
		t.Fatalf("hash match %d %v", len(p.Pairs), err)
	}

	// Sets and sorted sets page and filter too.
	for i := 0; i < 20; i++ {
		mr.SAdd("s", fmt.Sprintf("m%02d", i))
		mr.ZAdd("z", float64(i), fmt.Sprintf("m%02d", i))
	}
	p, err = st.GetValuePage(ctx, "s", 0, 0, 10, "m0*")
	if err != nil || len(p.Items) != 10 {
		t.Fatalf("set match %d %v", len(p.Items), err)
	}
	p, err = st.GetValuePage(ctx, "z", 0, 0, 10, "m1*")
	if err != nil || len(p.Pairs) != 10 || p.Pairs[0][0] != "m10" {
		t.Fatalf("zset match %+v %v", p.Pairs, err)
	}
}

func TestRowCRUD(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()

	mr.HSet("h", "a", "1")
	if err := st.HashSet(ctx, "h", "a", "b", "2"); err != nil {
		t.Fatal(err)
	}
	if mr.HGet("h", "a") != "" || mr.HGet("h", "b") != "2" {
		t.Fatalf("hash b=%q", mr.HGet("h", "b"))
	}
	mr.HSet("h", "c", "3")
	if err := st.HashSet(ctx, "h", "b", "c", "x"); err == nil {
		t.Fatal("hash field collision accepted")
	}
	if err := st.HashDel(ctx, "h", "b"); err != nil {
		t.Fatal(err)
	}
	if mr.HGet("h", "b") != "" {
		t.Fatal("field left")
	}

	mr.RPush("l", "a", "b", "a")
	if err := st.ListSet(ctx, "l", 1, "B"); err != nil {
		t.Fatal(err)
	}
	// Deleting the last "a" must not remove the first equal value.
	if err := st.ListDel(ctx, "l", 2); err != nil {
		t.Fatal(err)
	}
	items, _ := mr.List("l")
	if len(items) != 2 || items[0] != "a" || items[1] != "B" {
		t.Fatalf("list %v", items)
	}
	if err := st.ListPush(ctx, "l", "c"); err != nil {
		t.Fatal(err)
	}

	mr.SAdd("s", "a")
	if err := st.SetReplace(ctx, "s", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := mr.SIsMember("s", "a"); ok {
		t.Fatal("old member left")
	}
	if ok, _ := mr.SIsMember("s", "b"); !ok {
		t.Fatal("new member missing")
	}

	mr.ZAdd("z", 1, "a")
	mr.ZAdd("z", 2, "taken")
	if err := st.ZSetReplace(ctx, "z", "a", "taken", 2.5); err == nil {
		t.Fatal("zset member collision accepted")
	}
	if err := st.ZSetReplace(ctx, "z", "a", "b", 2.5); err != nil {
		t.Fatal(err)
	}
	if score, _ := mr.ZScore("z", "b"); score != 2.5 {
		t.Fatalf("score %v", score)
	}
	if err := st.ZSetAdd(ctx, "z", "c", 3); err != nil {
		t.Fatal(err)
	}
	if err := st.ZSetDel(ctx, "z", "c"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletePrefix(t *testing.T) {
	st, mr := testStore(t)
	for _, k := range []string{"user:1", "user:2", "user:10", "userx:1", "admin:1"} {
		mr.Set(k, "v")
	}
	n, err := st.DeletePrefix(context.Background(), "user:")
	if err != nil || n != 3 {
		t.Fatalf("deleted %d err %v", n, err)
	}
	if mr.Exists("user:1") || mr.Exists("user:2") || mr.Exists("user:10") {
		t.Fatal("namespace keys still exist")
	}
	if !mr.Exists("userx:1") || !mr.Exists("admin:1") {
		t.Fatal("delete prefix crossed the namespace boundary")
	}

	// Glob characters in the prefix are literal.
	mr.Set("a*b:1", "v")
	mr.Set("axb:1", "v")
	n, err = st.DeletePrefix(context.Background(), "a*b:")
	if err != nil || n != 1 {
		t.Fatalf("escaped delete %d err %v", n, err)
	}
	if mr.Exists("a*b:1") || !mr.Exists("axb:1") {
		t.Fatal("glob escaping failed")
	}

	if _, err := st.DeletePrefix(context.Background(), ""); err == nil {
		t.Fatal("empty prefix accepted")
	}
}

func TestHashScanCap(t *testing.T) {
	st, mr := testStore(t)
	for i := 0; i < 1200; i++ {
		mr.HSet("big", fmt.Sprintf("f%05d", i), "v")
	}
	v, err := st.GetValue(context.Background(), "big")
	if err != nil {
		t.Fatal(err)
	}
	if v.Size != 1200 || len(v.Pairs) != valueRowCap || !v.Truncated {
		t.Fatalf("size %d rows %d truncated %v", v.Size, len(v.Pairs), v.Truncated)
	}
	if v.Pairs[0][0] != "f00000" {
		t.Fatalf("first field %q", v.Pairs[0][0])
	}
}

func TestTunnelDialerAppliesTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		b := make([]byte, 1)
		if _, err := io.ReadFull(conn, b); err == nil {
			got <- b[0]
		}
	}()

	dial := tunnelDialer(ConnConfig{Addr: "127.0.0.1:6379", UseTLS: true},
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ln.Addr().String())
		})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "127.0.0.1:6379")
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("TLS handshake against a plain server succeeded")
	}
	select {
	case b := <-got:
		if b != 0x16 {
			t.Fatalf("first byte %#x, want a TLS handshake record", b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the server saw no TLS handshake")
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cases := map[string]string{
		"~":            home,
		"~/keys/id":    filepath.Join(home, "keys/id"),
		"/abs/key":     "/abs/key",
		"relative/key": "relative/key",
	}
	for in, want := range cases {
		if got := expandHome(in); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSSHHostKeyCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(knownhosts.Line([]string{"example.com"}, pub)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cb, err := sshHostKeyCallback(SSHConfig{KnownHosts: path})
	if err != nil {
		t.Fatal(err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
	if err := cb("example.com:22", remote, pub); err != nil {
		t.Fatalf("known key rejected: %v", err)
	}

	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ssh.NewPublicKey(otherPriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := cb("example.com:22", remote, other); err == nil {
		t.Fatal("unknown host key accepted")
	}
	if _, err := sshHostKeyCallback(SSHConfig{KnownHosts: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("a missing known_hosts file was accepted")
	}
	insecure, err := sshHostKeyCallback(SSHConfig{Insecure: true, KnownHosts: filepath.Join(dir, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if err := insecure("example.com:22", remote, other); err != nil {
		t.Fatalf("insecure callback rejected a key: %v", err)
	}
}

func TestCreateKey(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()

	if err := st.CreateKey(ctx, "str", "string", NewKeyValue{String: "v"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, _ := mr.Get("str"); got != "v" {
		t.Fatalf("string %q", got)
	}
	if mr.TTL("str") != time.Minute {
		t.Fatalf("ttl %v", mr.TTL("str"))
	}
	if err := st.CreateKey(ctx, "str", "string", NewKeyValue{String: "x"}, 0); err == nil {
		t.Fatal("existing key accepted")
	}

	if err := st.CreateKey(ctx, "h", "hash", NewKeyValue{Fields: map[string]string{"a": "1", "b": "2"}}, 0); err != nil {
		t.Fatal(err)
	}
	if got := mr.HGet("h", "b"); got != "2" {
		t.Fatalf("hash %q", got)
	}
	if err := st.CreateKey(ctx, "l", "list", NewKeyValue{Items: []string{"a", "b"}}, 0); err != nil {
		t.Fatal(err)
	}
	if n, _ := mr.List("l"); len(n) != 2 {
		t.Fatalf("list %v", n)
	}
	if err := st.CreateKey(ctx, "s", "set", NewKeyValue{Items: []string{"a", "b"}}, 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := mr.SIsMember("s", "b"); err != nil || !ok {
		t.Fatalf("set member missing: %v %v", ok, err)
	}
	if err := st.CreateKey(ctx, "z", "zset", NewKeyValue{Scores: map[string]float64{"m": 1.5}}, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := mr.ZScore("z", "m"); got != 1.5 {
		t.Fatalf("score %v", got)
	}
	if err := st.CreateKey(ctx, "bad", "stream", NewKeyValue{}, 0); err == nil {
		t.Fatal("unsupported type accepted")
	}
}

func TestRenameKey(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()
	mr.Set("old", "v")
	if err := st.RenameKey(ctx, "old", "new"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("old") || !mr.Exists("new") {
		t.Fatal("rename did not move the key")
	}
	mr.Set("other", "v")
	if err := st.RenameKey(ctx, "new", "other"); err == nil {
		t.Fatal("overwrote an existing key")
	}
}

func TestSetTTLAndFlush(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()
	mr.Set("k", "v")
	if err := st.SetTTL(ctx, "k", time.Hour); err != nil {
		t.Fatal(err)
	}
	if mr.TTL("k") != time.Hour {
		t.Fatalf("ttl %v", mr.TTL("k"))
	}
	if err := st.SetTTL(ctx, "k", 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTTL(ctx, "k", 0); err != nil { // already persistent
		t.Fatal(err)
	}
	if mr.TTL("k") != 0 {
		t.Fatalf("ttl after persist %v", mr.TTL("k"))
	}
	if err := st.FlushDB(ctx); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("k") {
		t.Fatal("flush left the key")
	}
}

func TestServerInfo(t *testing.T) {
	// miniredis has no "server" INFO section; the parse step is tested
	// directly and the error path here.
	st, _ := testStore(t)
	if _, err := st.ServerInfo(context.Background()); err == nil {
		t.Fatal("expected an unsupported-section error from miniredis")
	}
	var disconnected Store
	if _, err := disconnected.ServerInfo(context.Background()); err != errNotConnected {
		t.Fatalf("err %v", err)
	}

	info := parseServerInfo("# Server\r\nredis_version:7.2.4\r\nredis_mode:standalone\r\n")
	if info != "Redis 7.2.4 · standalone" {
		t.Fatalf("info %q", info)
	}
}

func TestTestConn(t *testing.T) {
	mr := miniredis.RunT(t)
	if _, err := testConn(ConnConfig{Addr: mr.Addr()}); err != nil {
		t.Fatal(err)
	}
}
