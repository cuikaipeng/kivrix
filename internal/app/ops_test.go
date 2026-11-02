package app

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestParseInfo(t *testing.T) {
	info := parseInfo("# Server\r\nredis_version:7.2.4\r\nos:Darwin\r\n\r\n# Keyspace\r\ndb0:keys=3,expires=1,avg_ttl=0\r\n")
	if got := infoValue(info, "server", "redis_version"); got != "7.2.4" {
		t.Fatalf("version %q", got)
	}
	if got := info["keyspace"]["db0"]; got != "keys=3,expires=1,avg_ttl=0" {
		t.Fatalf("keyspace %q", got)
	}
	keys, expires := parseKeyspace(info["keyspace"]["db0"])
	if keys != 3 || expires != 1 {
		t.Fatalf("parsed %d %d", keys, expires)
	}
}

func TestParseCommandLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`SET key value`, []string{"SET", "key", "value"}},
		{`SET "hello world" "x"`, []string{"SET", "hello world", "x"}},
		{`SET 'a b' c`, []string{"SET", "a b", "c"}},
		{`SET escaped\ value x`, []string{"SET", "escaped value", "x"}},
		{`SET "" x`, []string{"SET", "", "x"}},
	}
	for _, tc := range cases {
		got, err := parseCommandLine(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("%q -> %#v", tc.in, got)
		}
	}
	if _, err := parseCommandLine(`SET "oops`); err == nil {
		t.Fatal("unclosed quote accepted")
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("news, sports  updates")
	if len(got) != 3 || got[1] != "sports" {
		t.Fatalf("%#v", got)
	}
}

func TestFormatReply(t *testing.T) {
	if got := formatReply(int64(42)); got != "42" {
		t.Fatalf("%q", got)
	}
	if got := formatReply([]any{"a", int64(2)}); got != "1) a\n2) 2" {
		t.Fatalf("%q", got)
	}
	if got := formatReply([]any{}); got != "(empty list or set)" {
		t.Fatalf("%q", got)
	}
	if got := formatReply(nil); got != "(nil)" {
		t.Fatalf("%q", got)
	}
}

func TestCLIBlocked(t *testing.T) {
	for _, name := range []string{"MONITOR", "SUBSCRIBE", "MULTI", "AUTH"} {
		if cliBlocked(name) == "" {
			t.Fatalf("%s not blocked", name)
		}
	}
	if cliBlocked("GET") != "" || cliBlocked("SELECT") != "" {
		t.Fatal("GET blocked")
	}
}

func TestCommandHookRedactsAuth(t *testing.T) {
	var got CommandEntry
	h := &commandHook{record: func(e CommandEntry) { got = e }}
	next := func(ctx context.Context, cmd redis.Cmder) error { return nil }
	cmd := redis.NewCmd(context.Background(), "AUTH", "hunter2")
	if err := h.ProcessHook(next)(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if got.Command() != "AUTH [redacted]" {
		t.Fatalf("command %q", got.Command())
	}
}

func TestRESPHelpers(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCommand(&buf, "GET", "k"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "*2\r\n$3\r\nGET\r\n$1\r\nk\r\n" {
		t.Fatalf("write %q", buf.String())
	}
	r := bufio.NewReader(strings.NewReader("+OK\r\n$5\r\nhello\r\n-ERR bad\r\n*2\r\n$1\r\na\r\n:2\r\n"))
	for _, want := range []any{"OK", "hello"} {
		got, err := readReply(r)
		if err != nil || got != want {
			t.Fatalf("reply %#v %v", got, err)
		}
	}
	if _, err := readReply(r); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error reply %v", err)
	}
	got, err := readReply(r)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.([]any)
	if !ok || len(arr) != 2 || arr[0] != "a" || arr[1] != int64(2) {
		t.Fatalf("array %#v", got)
	}
}
