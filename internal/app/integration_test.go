//go:build integration

package app

// Real Redis integration tests. Run scripts/integration.sh, or set the
// REDIS_PILOT_TEST_* variables and run:
//
//	go test -tags=integration -run Integration -v

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func integrationStore(t *testing.T, cfg ConnConfig) *Store {
	t.Helper()
	var st Store
	if err := st.Connect(cfg); err != nil {
		t.Fatalf("connect %s: %v", cfg.Name, err)
	}
	t.Cleanup(st.Close)
	return &st
}

func integrationAddr(t *testing.T, env string) string {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv(env))
	if addr == "" {
		t.Skipf("%s is not set", env)
	}
	return addr
}

func TestIntegrationStandalone(t *testing.T) {
	addr := integrationAddr(t, "REDIS_PILOT_TEST_STANDALONE")
	ctx := context.Background()
	st := integrationStore(t, ConnConfig{Name: "standalone", Addr: addr})
	prefix := fmt.Sprintf("rp-it-%d", time.Now().UnixNano())
	key := prefix + ":value"
	stream := prefix + ":stream"
	defer st.DeleteKeys(ctx, key, stream)

	if err := st.SetString(ctx, key, "hello"); err != nil {
		t.Fatal(err)
	}
	v, err := st.GetValue(ctx, key)
	if err != nil || v.Str != "hello" {
		t.Fatalf("get %+v %v", v, err)
	}
	if err := st.SetTTL(ctx, key, time.Minute); err != nil {
		t.Fatal(err)
	}
	v, _ = st.GetValue(ctx, key)
	if v.TTL <= 0 {
		t.Fatalf("ttl %v", v.TTL)
	}
	keys, _, err := st.ScanPage(ctx, "", prefix+":*", 100)
	if err != nil || len(keys) == 0 {
		t.Fatalf("scan %v %v", keys, err)
	}
	info, err := st.Info(ctx)
	if err != nil || !strings.Contains(info, "redis_version") {
		t.Fatalf("info %q %v", info, err)
	}
	clients, err := st.ClientList(ctx)
	if err != nil || !strings.Contains(clients, "addr=") {
		t.Fatalf("clients %q %v", clients, err)
	}

	if err := st.StreamGroupCreate(ctx, stream, "workers", "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StreamAdd(ctx, stream, "*", [][2]string{{"kind", "start"}}); err != nil {
		t.Fatal(err)
	}
	groups, err := st.StreamGroups(ctx, stream)
	if err != nil || len(groups) != 1 || groups[0].Name != "workers" {
		t.Fatalf("groups %+v %v", groups, err)
	}
	if err := st.StreamGroupDestroy(ctx, stream, "workers"); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationSentinel(t *testing.T) {
	sentinels := integrationAddr(t, "REDIS_PILOT_TEST_SENTINEL")
	master := os.Getenv("REDIS_PILOT_TEST_SENTINEL_MASTER")
	if master == "" {
		t.Skip("REDIS_PILOT_TEST_SENTINEL_MASTER is not set")
	}
	ctx := context.Background()
	names, err := sentinelMasters(ConnConfig{SentinelAddrs: []string{sentinels}})
	if err != nil || !slices.Contains(names, master) {
		t.Fatalf("discover masters: %v %v", names, err)
	}
	st := integrationStore(t, ConnConfig{
		Name: "sentinel", Mode: "sentinel",
		SentinelAddrs: []string{sentinels}, MasterName: master,
	})
	key := fmt.Sprintf("rp-it-%d:sentinel", time.Now().UnixNano())
	defer st.DeleteKeys(ctx, key)
	if err := st.SetString(ctx, key, "sentinel"); err != nil {
		t.Fatal(err)
	}
	v, err := st.GetValue(ctx, key)
	if err != nil || v.Str != "sentinel" {
		t.Fatalf("get %+v %v", v, err)
	}
}

func TestIntegrationCluster(t *testing.T) {
	addrs := integrationAddr(t, "REDIS_PILOT_TEST_CLUSTER")
	ctx := context.Background()
	st := integrationStore(t, ConnConfig{
		Name: "cluster", Mode: "cluster", ClusterAddrs: strings.Split(addrs, ","),
	})
	prefix := fmt.Sprintf("rp-it-%d", time.Now().UnixNano())
	keyA, keyB := prefix+":a", prefix+":b"
	defer st.DeleteKeys(ctx, keyA, keyB)
	if err := st.SetString(ctx, keyA, "cross-slot"); err != nil {
		t.Fatal(err)
	}
	if err := st.RenameKey(ctx, keyA, keyB); err != nil {
		t.Fatal(err)
	}
	v, err := st.GetValue(ctx, keyB)
	if err != nil || v.Str != "cross-slot" {
		t.Fatalf("renamed %+v %v", v, err)
	}
	if _, err := st.DeleteKeys(ctx, keyB); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationStreamGroupOperations(t *testing.T) {
	addr := integrationAddr(t, "REDIS_PILOT_TEST_STANDALONE")
	ctx := context.Background()
	st := integrationStore(t, ConnConfig{Name: "standalone", Addr: addr})
	key := fmt.Sprintf("rp-it-%d:stream-ops", time.Now().UnixNano())
	defer st.DeleteKeys(ctx, key)
	if err := st.StreamGroupCreate(ctx, key, "workers", "0"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := st.StreamAdd(ctx, key, fmt.Sprintf("%d-0", i), [][2]string{{"n", fmt.Sprint(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "workers", Consumer: "c1", Streams: []string{key, ">"}, Count: 2,
	}).Result(); err != nil {
		t.Fatal(err)
	}
	if n, _, err := st.StreamAutoClaim(ctx, key, "workers", "c2", 0, "0-0", 10); err != nil || n != 2 {
		t.Fatalf("autoclaim %d %v", n, err)
	}
	if n, err := st.StreamTrim(ctx, key, "MAXLEN", "1", false); err != nil || n != 2 {
		t.Fatalf("trim %d %v", n, err)
	}
	if n, err := st.StreamConsumerDelete(ctx, key, "workers", "c2"); err != nil || n != 2 {
		t.Fatalf("consumer delete %d %v", n, err)
	}
}

func TestIntegrationParityFlows(t *testing.T) {
	for _, mode := range []string{"standalone", "cluster"} {
		t.Run(mode, func(t *testing.T) {
			cfg := ConnConfig{Mode: mode}
			if mode == "cluster" {
				cfg.ClusterAddrs = strings.Split(integrationAddr(t, "REDIS_PILOT_TEST_CLUSTER"), ",")
			} else {
				cfg.Addr = integrationAddr(t, "REDIS_PILOT_TEST_STANDALONE")
			}
			st := integrationStore(t, cfg)
			ctx := context.Background()
			prefix := fmt.Sprintf("kivrix-parity-%d:", time.Now().UnixNano())
			key, hash := prefix+"*", prefix+"hash"
			defer st.DeleteKeys(ctx, key, hash)
			codec := valueCodec{format: "JSON", decode: "GZip"}
			encoded, err := codec.save(`{"n":9007199254740993}`)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.client.Set(ctx, key, encoded, time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			raw, _ := st.client.Get(ctx, key).Result()
			decoded, resolved, err := codec.open(raw)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err = resolved.save(strings.ReplaceAll(decoded, "9007199254740993", "9007199254740994"))
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetString(ctx, key, string(encoded)); err != nil {
				t.Fatal(err)
			}
			v, err := st.GetValue(ctx, key)
			decoded, _, decodeErr := codec.open(v.Str)
			if err != nil || decodeErr != nil || v.TTL <= 0 || !strings.Contains(decoded, "9007199254740994") {
				t.Fatalf("encoded save and TTL: %+v %v %v", v, err, decodeErr)
			}
			if err := st.addRows(ctx, rowEdit{key: hash, typ: "hash", bulkText: "a=one\nb=two", codec: valueCodec{decode: "Base64"}}); err != nil {
				t.Fatal(err)
			}
			var got []string
			cursor := ""
			for {
				batch, next, err := st.scanFiltered(ctx, cursor, keyFilter{prefix + "*", "Pattern", "hash"}, 1)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, batch...)
				if next == "" {
					break
				}
				cursor = next
			}
			if len(got) != 1 || got[0] != hash {
				t.Fatalf("SCAN TYPE: %v", got)
			}
			counts, err := st.Keyspace(ctx)
			if err != nil || counts[0] < 2 {
				t.Fatalf("keyspace count: %v %v", counts, err)
			}
			got, _, err = st.scanFiltered(ctx, "", keyFilter{key, "Exact", "string"}, 1)
			if err != nil || len(got) != 1 || got[0] != key {
				t.Fatalf("literal key: %v %v", got, err)
			}
		})
	}
}

func TestIntegrationUnixAndTimeout(t *testing.T) {
	addr := integrationAddr(t, "REDIS_PILOT_TEST_UNIX")
	st := integrationStore(t, ConnConfig{Addr: addr, ConnectionOptions: ConnectionOptions{Network: "unix", CommandTimeout: 1}})
	ctx, cancel := st.commandContext()
	start := time.Now()
	_, err := st.Do(ctx, "BLPOP", fmt.Sprintf("kivrix-absent-%d", start.UnixNano()), "4")
	cancel()
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("command deadline: %v after %v", err, time.Since(start))
	}
	if err := st.SelectDB(context.Background(), 2); err != nil || st.cfg.DB != 2 {
		t.Fatalf("select DB over socket: %d %v", st.cfg.DB, err)
	}
	// A socket connection must also work for the dedicated MONITOR stream.
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lines, done := make(chan string, 1), make(chan error, 1)
	go func() {
		done <- st.Monitor(ctx, func(line string) {
			select {
			case lines <- line:
			default:
			}
		})
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			st.client.Ping(ctx)
		case line := <-lines:
			if strings.Contains(strings.ToLower(line), "ping") {
				cancel()
				<-done
				return
			}
		case err := <-done:
			t.Fatalf("socket monitor: %v", err)
		}
	}
}
