package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/redis/go-redis/v9"
)

func TestReplyAllocationLimits(t *testing.T) {
	for name, input := range map[string]string{
		"bulk":       "$99999999999\r\n",
		"array":      "*99999999999\r\n",
		"map":        "%60000\r\n",
		"depth":      strings.Repeat("*1\r\n", 65) + "+OK\r\n",
		"attributes": strings.Repeat("|0\r\n", 65) + "+OK\r\n",
		"line":       "+" + strings.Repeat("x", maxReplyLine) + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := &replyReader{r: bufio.NewReader(strings.NewReader(input))}
			if _, err := io.Copy(io.Discard, r); !errors.Is(err, errReplyLimit) {
				t.Fatalf("expected safety limit, got %v", err)
			}
		})
	}
}

func TestReplyGuardPreservesRESP(t *testing.T) {
	// Pipelined RESP2 and RESP3, including empty values and attributed values.
	input := "+OK\r\n$5\r\nhello\r\n$0\r\n\r\n$-1\r\n*0\r\n" +
		"*2\r\n:42\r\n%1\r\n+key\r\n#t\r\n" +
		"*2\r\n|1\r\n+ttl\r\n:5\r\n+value\r\n,1.5\r\n" +
		"~1\r\n_\r\n>2\r\n+message\r\n$3\r\nhey\r\n"
	r := &replyReader{r: bufio.NewReader(strings.NewReader(input))}
	var got bytes.Buffer
	// One byte per consumer read exercises headers and bulk boundaries.
	if _, err := io.CopyBuffer(struct{ io.Writer }{&got}, struct{ io.Reader }{r}, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if got.String() != input {
		t.Fatalf("RESP bytes changed: %q", got.String())
	}
}

func TestLargeRedisValueIsRejectedWithoutTruncating(t *testing.T) {
	st, mr := testStore(t)
	mr.Set("big", strings.Repeat("x", maxReplyBytes+1))
	if _, err := st.GetValue(context.Background(), "big"); !errors.Is(err, errValueLimit) {
		t.Fatalf("editor accepted oversized value: %v", err)
	}
	if _, err := st.Do(context.Background(), "GET", "big"); !errors.Is(err, errReplyLimit) {
		t.Fatalf("client accepted oversized reply: %v", err)
	}
	if got, err := st.Do(context.Background(), "PING"); err != nil || got != "PONG" {
		t.Fatalf("connection did not recover: %v %v", got, err)
	}
}

func TestSensitiveCommandsNeverEnterHistoryOrStoredLog(t *testing.T) {
	s := &session{}
	for _, line := range []string{
		"AUTH hunter2", "HELLO 3 AUTH default hunter2", "ACL SETUSER user >hunter2",
		"CONFIG SET requirepass hunter2", "SENTINEL SET service auth-pass hunter2",
		"MIGRATE host 6379 key 0 100 AUTH hunter2",
	} {
		args, _ := parseCommandLine(line)
		values := make([]any, len(args))
		for i := range args {
			values[i] = args[i]
		}
		var entry CommandEntry
		h := &commandHook{record: func(e CommandEntry) { entry = e }}
		h.entry(redis.NewCmd(context.Background(), values...), 0, errors.New("hunter2 failed"))
		if strings.Contains(fmt.Sprint(entry), "hunter2") {
			t.Fatal("secret retained in command entry")
		}
		s.rememberCLI(line)
	}
	if len(s.cliHistory) != 0 {
		t.Fatalf("sensitive history retained: %d", len(s.cliHistory))
	}
	s.rememberCLI("GET key")
	if len(s.cliHistory) != 1 {
		t.Fatal("ordinary command history lost")
	}
}

func TestLiveQueueBoundsBacklog(t *testing.T) {
	posted := make(chan func(), 4)
	var items []int
	var dropped int
	q := &eventQueue[int]{limit: 128, post: func(f func()) { posted <- f }, apply: func(v []int, n int) { items, dropped = v, n }}
	defer q.close()
	for i := 0; i < 100000; i++ {
		q.push(i)
	}
	var apply func()
	select {
	case apply = <-posted:
	case <-time.After(2 * time.Second):
		t.Fatal("no UI update")
	}
	for i := 100000; i < 200000; i++ {
		q.push(i)
	}
	q.mu.Lock()
	retained := len(q.items)
	q.mu.Unlock()
	if retained != 128 || len(posted) != 0 {
		t.Fatalf("unbounded backlog: %d items, %d callbacks", retained, len(posted))
	}
	apply()
	if len(items) != 128 || items[0] != 200000-128 || items[127] != 199999 || dropped != 200000-128 {
		t.Fatalf("wrong retained tail: %d items, %d dropped", len(items), dropped)
	}
	// Exercise producer/close synchronization under the race detector.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for n := 0; n < 1000; n++ {
				q.push(n)
			}
		})
	}
	q.close()
	wg.Wait()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) != 0 {
		t.Fatal("closed queue retained messages")
	}
}

func TestLiveFeedRateBudget(t *testing.T) {
	var b liveBudget
	now := time.Now()
	for range 4096 {
		if !b.allow(1, now) {
			t.Fatal("ordinary batch refused")
		}
	}
	if b.allow(1, now) {
		t.Fatal("message flood accepted")
	}
	if !b.allow(4<<20, now.Add(time.Second)) {
		t.Fatal("budget failed to reset")
	}
	if b.allow(1, now.Add(time.Second)) {
		t.Fatal("byte flood accepted")
	}
}

func BenchmarkSecurityLoadedKeyFrame(b *testing.B) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	defer s.close()
	for i := 0; i < maxLoadedKeys; i++ {
		s.keys.add(fmt.Sprintf("key%06d", i))
	}
	tt := ui.NewTester(a.view, 1180, 760)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tt.Frame()
	}
}

func TestKeyTreeBudgets(t *testing.T) {
	tree := newKeyTree(":")
	deep := strings.Repeat("a:", 200) + "leaf"
	if !tree.add(deep) || len(tree.byPath) > 33 || !tree.node(deep).isKey {
		t.Fatal("deep key was not folded safely")
	}
	tree.remove(deep)
	if tree.bytes != 0 || tree.count != 0 {
		t.Fatal("tree did not release budget")
	}
	if tree.add(strings.Repeat("x", maxTreeBytes)) || tree.count != 0 {
		t.Fatal("oversized key accepted")
	}
	for i := 0; i < maxLoadedKeys; i++ {
		if !tree.add(fmt.Sprintf("k%06d", i)) {
			t.Fatal("ordinary key rejected")
		}
	}
	if tree.add("one-more") {
		t.Fatal("key count limit not enforced")
	}
}

func TestHostileDecoders(t *testing.T) {
	for name, tc := range map[string]struct{ decode, value string }{
		"msgpack length":     {"Msgpack", "\xdd\xff\xff\xff\xff"},
		"msgpack depth":      {"Msgpack", strings.Repeat("\x91", 100) + "\xc0"},
		"php length":         {"PHP", `a:999999999:{}`},
		"pickle frame":       {"Pickle", "\x80\x04\x95\xff\xff\xff\xff\xff\xff\xff\x7f."},
		"pickle cycle":       {"Pickle", "\x80\x02]q\x00h\x00a."},
		"pickle constructor": {"Pickle", "cos\nsystem\n(S'echo unsafe'\ntR."},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeValue([]byte(tc.value), tc.decode); err == nil {
				t.Fatal("unsafe value accepted")
			}
		})
	}
	if _, err := formatValue(strings.Repeat("[", 100)+"0"+strings.Repeat("]", 100), "JSON", "None"); err == nil {
		t.Fatal("deep JSON accepted")
	}
	if _, err := formatValue("a: &a [*a]", "YAML", "None"); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("YAML cycle: %v", err)
	}
	if _, err := runCustomDecoder("echo {VALUE}", []byte("password")); err == nil {
		t.Fatal("value exposed through argv")
	}
	// Reply formatting stops while traversing, even for cyclic structures.
	cycle := make([]any, 1)
	cycle[0] = cycle
	if got := formatReply(cycle); len(got) > maxCLIResult+32 || !strings.Contains(got, "truncated") {
		t.Fatal("unbounded reply formatting")
	}
}

func TestBoundedFilesAndPrivateOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "export.txt")
	if err := os.WriteFile(p, []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(p, []byte("private")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("export kept public permissions")
	}
	if _, err := readBoundedFile(p, 3); err == nil {
		t.Fatal("oversized file accepted")
	}
	// A quoted CSV record must not accumulate an arbitrarily large field.
	if err := os.WriteFile(p, []byte("key,type,value\nk,string,\""+strings.Repeat("x", maxReplyBytes+8192)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := importSessionCSVWithOptions(context.Background(), &Store{}, p, defaultCSVImportOptions()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unbounded CSV: %v", err)
	}
}

func TestUpdateRejectsHTTPSDowngrade(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/installer", http.StatusFound)
	}))
	defer srv.Close()
	if _, err := updateHTTPClient(srv.Client()).Get(srv.URL); err == nil || !strings.Contains(err.Error(), "not HTTPS") {
		t.Fatalf("insecure redirect: %v", err)
	}
}

func TestCustomDecoderCancellationAndOutputLimit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runCustomDecoderContext(ctx, "sh -c 'sleep 30 & wait'", nil); err == nil {
		t.Fatal("decoder ignored cancellation")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("decoder helpers kept pipes open")
	}
	if _, err := runCustomDecoder("sh -c 'head -c 9000000 /dev/zero'", nil); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("unbounded decoder output: %v", err)
	}
}

func TestReadonlyDecoderDoesNotRunDuringRendering(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires sh")
	}
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	defer s.close()
	marker := filepath.Join(t.TempDir(), "executed")
	a.prefs.CustomDecoders = []CustomDecoder{{Name: "audit", Command: "sh -c 'touch \"" + marker + "\"'"}}
	s.valKey, s.valFormat, s.valDecode = "k", "Raw", "Custom: audit"
	s.val = Value{Type: "string", Str: "hello"}
	tt := ui.NewTester(a.view, 1180, 760)
	for range 10 {
		tt.Frame()
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("rendering executed a custom program")
	}
}

func TestLargeLiveViewsVirtualizeText(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	defer s.close()
	text := strings.Repeat("x", maxLiveText)
	for i := 0; i < maxMonitorRows; i++ {
		s.appendMonitors([]string{fmt.Sprintf("%d %s", i, text)})
	}
	for i := 0; i < maxPubRows; i++ {
		s.appendPubs([]pubMessage{{channel: "channel", payload: text}})
	}
	for i := 0; i < maxCommandRows; i++ {
		s.appendCommands([]CommandEntry{{Name: "SET", Args: []string{"key", text}}})
	}
	for i := 0; i < maxCLIEntries; i++ {
		s.appendCLI(cliEntry{command: "GET key", result: text})
	}
	tt := ui.NewTester(a.view, 1180, 760)
	for range 2 {
		for _, tool := range []string{"monitor", "pubsub", "cmdlog", "cli", "browser"} {
			s.tool = tool
			tt.Frame()
			tt.Frame()
			size := 0
			for _, text := range tt.Texts() {
				size += len(text)
			}
			if size > 128<<10 {
				t.Fatalf("%s builds offscreen log text: %d bytes", tool, size)
			}
		}
	}
}

func TestClosedWindowRejectsLateConnection(t *testing.T) {
	a := newApp()
	a.closed = true
	st, _ := testStore(t)
	a.connectedTo(Profile{ID: "late"}, *st, "", SecretSet{})
	if len(a.sessions) != 0 {
		t.Fatal("closed window adopted a connection")
	}
	if _, err := st.Do(context.Background(), "PING"); err == nil {
		t.Fatal("late connection leaked")
	}
}

func FuzzReplyGuard(f *testing.F) {
	for _, seed := range []string{"+OK\r\n", "$99999999999\r\n", "*1\r\n:5\r\n", "|0\r\n+OK\r\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > maxReplyLine*2 {
			t.Skip()
		}
		r := &replyReader{r: bufio.NewReader(bytes.NewReader(b))}
		_, _ = io.Copy(io.Discard, r)
	})
}

func FuzzDecodeLengthGuards(f *testing.F) {
	for _, seed := range []string{"\xc0", "a:1:{i:0;N;}", "\x80\x02]q\x00h\x00a."} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			t.Skip()
		}
		_ = checkSerialized(b, "Msgpack")
		_ = checkSerialized(b, "PHP")
		_ = checkPickle(b)
	})
}
