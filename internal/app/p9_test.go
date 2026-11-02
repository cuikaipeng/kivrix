package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/redis/go-redis/v9"
)

func TestParseClientList(t *testing.T) {
	raw := "id=1 addr=127.0.0.1:5000 name=app db=0 age=12 idle=2 cmd=client\n" +
		"id=2 addr=10.0.0.2:5001 name= db=1 age=3 idle=0 cmd=get\n"
	rows := parseClientList(raw)
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].addr != "127.0.0.1:5000" || rows[0].name != "app" || rows[0].cmd != "client" {
		t.Fatalf("row %+v", rows[0])
	}
	if rows[1].db != "1" {
		t.Fatalf("row %+v", rows[1])
	}
}

func TestOpsFilters(t *testing.T) {
	s := &session{
		slowEntries:  []slowEntry{{command: "HGETALL big", client: "app"}, {command: "GET small", client: "web"}},
		monitorLines: []string{"app GET a", "web SET b"},
		pubMessages:  []pubMessage{{channel: "news", payload: "hello"}, {channel: "sports", payload: "goal"}},
		cmdLog:       []CommandEntry{{Name: "get"}, {Name: "set"}},
	}
	s.slowFilter = "app"
	if got := s.filteredSlow(); len(got) != 1 || !strings.Contains(got[0].command, "HGETALL") {
		t.Fatalf("slow %+v", got)
	}
	s.monitorFilter = "SET"
	if got := s.filteredMonitor(); len(got) != 1 || !strings.Contains(got[0], "SET") {
		t.Fatalf("monitor %+v", got)
	}
	s.pubFilter = "goal"
	if got := s.filteredPub(); len(got) != 1 || got[0].channel != "sports" {
		t.Fatalf("pub %+v", got)
	}
	s.cmdFilter = "get"
	if got := s.filteredCommands(); len(got) != 1 || got[0].Name != "get" {
		t.Fatalf("cmd %+v", got)
	}
}

func TestStreamFilterAndRowMapping(t *testing.T) {
	a := newApp()
	s := a.sessionsForTest("events")
	s.val = Value{Type: "stream", Stream: []StreamEntry{
		{ID: "3-0", Fields: [][2]string{{"kind", "stop"}}},
		{ID: "2-0", Fields: [][2]string{{"kind", "start"}}},
	}}
	s.valQuery = "start"
	indices := s.filteredStream()
	if len(indices) != 1 || indices[0] != 1 {
		t.Fatalf("indices %v", indices)
	}
	s.streamIndices = indices
	r := a.rowAt(s, 0)
	if r.streamID != "2-0" || r.index != 1 {
		t.Fatalf("row %+v", r)
	}
}

func (a *App) sessionsForTest(key string) *session {
	s := newSession(a, Profile{ID: "p", Name: "P"}, Store{cfg: ConnConfig{Name: "P", Addr: "p:6379"}}, "")
	a.sessions = []*session{s}
	a.active = 0
	s.valKey = key
	return s
}

func TestStreamBefore(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	entries := []StreamEntry{
		{ID: "1-0", Fields: [][2]string{{"n", "1"}}},
		{ID: "2-0", Fields: [][2]string{{"n", "2"}}},
		{ID: "3-0", Fields: [][2]string{{"n", "3"}}},
	}
	if err := st.CreateKey(ctx, "events", "stream", NewKeyValue{Stream: entries}, 0); err != nil {
		t.Fatal(err)
	}
	got, err := st.StreamBefore(ctx, "events", "3-0", 1)
	if err != nil || len(got) != 1 || got[0].ID != "2-0" {
		t.Fatalf("before %+v %v", got, err)
	}
	got, err = st.StreamBefore(ctx, "events", "2-0", 5)
	if err != nil || len(got) != 1 || got[0].ID != "1-0" {
		t.Fatalf("before all %+v %v", got, err)
	}
}

func TestPublish(t *testing.T) {
	st, _ := testStore(t)
	n, err := st.Publish(context.Background(), "news", "hello")
	if err != nil || n != 0 {
		t.Fatalf("publish %d %v", n, err)
	}
}

func TestStreamGroups(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.StreamGroupCreate(ctx, "events", "workers", "0"); err != nil {
		t.Fatal(err)
	}
	groups, err := st.StreamGroups(ctx, "events")
	if err != nil || len(groups) != 1 || groups[0].Name != "workers" {
		t.Fatalf("groups %+v %v", groups, err)
	}
	if err := st.StreamGroupDestroy(ctx, "events", "workers"); err != nil {
		t.Fatal(err)
	}
	groups, err = st.StreamGroups(ctx, "events")
	if err != nil || len(groups) != 0 {
		t.Fatalf("after destroy %+v %v", groups, err)
	}
}

func TestStatusHistory(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.statusInfo = parseInfo("# Clients\nconnected_clients:2\n# Memory\nused_memory:1536\n# Stats\ninstantaneous_input_kbps:1.5\ninstantaneous_output_kbps:2.5\n")
	s.addStatusSample()
	if len(s.statusHistory) != 1 || s.statusHistory[0].clients != 2 || s.statusHistory[0].memory != 1536 {
		t.Fatalf("history %+v", s.statusHistory)
	}
	for i := 0; i < maxStatusHistory+5; i++ {
		s.addStatusSample()
	}
	if len(s.statusHistory) != maxStatusHistory {
		t.Fatalf("cap %d", len(s.statusHistory))
	}
}

func TestStatusSamplingStopsWhenLeaving(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "status"
	s.statusSampling = true
	s.statusTimer = time.AfterFunc(time.Hour, func() {})
	s.setTool("browser")
	if s.statusSampling || s.statusTimer != nil {
		t.Fatal("status sampling did not stop")
	}
}

func TestStatusSamplingNeedsConnection(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.toggleStatusSampling()
	if s.statusSampling {
		t.Fatal("sampling started without a connection")
	}
	if s.status.key == "" {
		t.Fatal("no explanation")
	}
}

func TestStreamConsumersPendingAck(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.StreamGroupCreate(ctx, "events", "workers", "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.client.XAdd(ctx, &redis.XAddArgs{Stream: "events", ID: "1-0", Values: []string{"kind", "start"}}).Result(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "workers", Consumer: "c1", Streams: []string{"events", ">"}, Count: 1,
	}).Result(); err != nil {
		t.Fatal(err)
	}
	consumers, err := st.StreamConsumers(ctx, "events", "workers")
	if err != nil || len(consumers) != 1 || consumers[0].Name != "c1" || consumers[0].Pending != 1 {
		t.Fatalf("consumers %+v %v", consumers, err)
	}
	pending, err := st.StreamPending(ctx, "events", "workers", 10)
	if err != nil || len(pending) != 1 || pending[0].ID != "1-0" {
		t.Fatalf("pending %+v %v", pending, err)
	}
	n, err := st.StreamAck(ctx, "events", "workers", "1-0")
	if err != nil || n != 1 {
		t.Fatalf("ack %d %v", n, err)
	}
	pending, _ = st.StreamPending(ctx, "events", "workers", 10)
	if len(pending) != 0 {
		t.Fatalf("pending after ack %+v", pending)
	}
}

func TestGroupDestroyConfirmation(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "events"
	s.val = Value{Type: "stream"}
	a.askDestroyStreamGroup(s, "workers")
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Destroy consumer group?") || !tt.HasText("workers") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestStreamTrimAndConsumerDelete(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := st.client.XAdd(ctx, &redis.XAddArgs{Stream: "events", ID: fmt.Sprintf("%d-0", i+1), Values: []string{"n", fmt.Sprint(i)}}).Result(); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.StreamTrim(ctx, "events", "MAXLEN", "2", false)
	if err != nil || n != 3 {
		t.Fatalf("trim %d %v", n, err)
	}
	p, err := st.GetValuePage(ctx, "events", 0, 0, 10, "")
	if err != nil || p.Size != 2 {
		t.Fatalf("after trim %+v %v", p, err)
	}
	if err := st.StreamGroupCreate(ctx, "events", "workers", "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "workers", Consumer: "c1", Streams: []string{"events", ">"}, Count: 1,
	}).Result(); err != nil {
		t.Fatal(err)
	}
	if n, err := st.StreamConsumerDelete(ctx, "events", "workers", "c1"); err != nil || n != 1 {
		t.Fatalf("delete consumer %d %v", n, err)
	}
	consumers, err := st.StreamConsumers(ctx, "events", "workers")
	if err != nil || len(consumers) != 0 {
		t.Fatalf("consumers %+v %v", consumers, err)
	}
}

func TestStreamPendingPage(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.StreamGroupCreate(ctx, "events", "workers", "0"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.client.XAdd(ctx, &redis.XAddArgs{Stream: "events", ID: fmt.Sprintf("%d-0", i+1), Values: []string{"n", fmt.Sprint(i)}}).Result(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "workers", Consumer: "c1", Streams: []string{"events", ">"}, Count: 3,
	}).Result(); err != nil {
		t.Fatal(err)
	}
	first, err := st.StreamPendingPage(ctx, "events", "workers", "-", 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first %+v %v", first, err)
	}
	second, err := st.StreamPendingPage(ctx, "events", "workers", first[0].ID, 1)
	if err != nil || len(second) != 1 || second[0].ID == first[0].ID {
		t.Fatalf("second %+v %v", second, err)
	}
}

func TestStreamAutoClaim(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.StreamGroupCreate(ctx, "events", "workers", "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.client.XAdd(ctx, &redis.XAddArgs{Stream: "events", ID: "1-0", Values: []string{"n", "1"}}).Result(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "workers", Consumer: "c1", Streams: []string{"events", ">"}, Count: 1,
	}).Result(); err != nil {
		t.Fatal(err)
	}
	n, _, err := st.StreamAutoClaim(ctx, "events", "workers", "c2", 0, "0-0", 10)
	if err != nil || n != 1 {
		t.Fatalf("autoclaim %d %v", n, err)
	}
	pending, err := st.StreamPending(ctx, "events", "workers", 10)
	if err != nil || len(pending) != 1 || pending[0].Consumer != "c2" {
		t.Fatalf("pending %+v %v", pending, err)
	}
}
