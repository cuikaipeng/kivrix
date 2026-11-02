package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCSVValueRoundTrip(t *testing.T) {
	values := []Value{
		{Type: "string", Str: "a\nb,\"c\""},
		{Type: "hash", Pairs: [][2]string{{"a", "1"}, {"b", "2"}}},
		{Type: "list", Items: []string{"a", "b"}},
		{Type: "set", Items: []string{"a", "b"}},
		{Type: "zset", Pairs: [][2]string{{"a", "1.5"}, {"b", "2"}}},
		{Type: "stream", Stream: []StreamEntry{{ID: "1-1", Fields: [][2]string{{"kind", "start"}}}}},
		{Type: "json", Str: `{"a":1}`},
	}
	for _, v := range values {
		enc, err := encodeCSVValue(v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeCSVValue(v.Type, enc)
		if err != nil {
			t.Fatalf("%s: %v", v.Type, err)
		}
		switch v.Type {
		case "string", "json":
			if got.String != v.Str {
				t.Fatalf("%s %q", v.Type, got.String)
			}
		case "hash":
			if got.Fields["a"] != "1" {
				t.Fatalf("hash %v", got.Fields)
			}
		case "list", "set":
			if len(got.Items) != 2 {
				t.Fatalf("%s %v", v.Type, got.Items)
			}
		case "zset":
			if got.Scores["a"] != 1.5 {
				t.Fatalf("zset %v", got.Scores)
			}
		case "stream":
			if len(got.Stream) != 1 || got.Stream[0].ID != "1-1" {
				t.Fatalf("stream %+v", got.Stream)
			}
		}
	}
}

func TestCSVImportExport(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	fields := map[string]string{"name": "Ada", "role": "admin"}
	if err := st.CreateKey(ctx, "user:1", "hash", NewKeyValue{Fields: fields}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateKey(ctx, "queue", "list", NewKeyValue{Items: []string{"a", "b"}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateKey(ctx, "events", "stream", NewKeyValue{Stream: []StreamEntry{{Fields: [][2]string{{"kind", "start"}}}}}, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.csv")
	n, skipped, err := exportSessionCSVWithTTL(ctx, st, path, []string{"user:1", "queue", "events"}, true)
	if err != nil || n != 3 || skipped != 0 {
		t.Fatalf("export %d %d %v", n, skipped, err)
	}
	if _, err := st.DeleteKeys(ctx, "user:1", "queue", "events"); err != nil {
		t.Fatal(err)
	}
	n, skipped, err = importSessionCSVWithOptions(ctx, st, path, defaultCSVImportOptions())
	if err != nil || n != 3 || skipped != 0 {
		t.Fatalf("import %d %d %v", n, skipped, err)
	}
	got, err := st.GetValue(ctx, "user:1")
	if err != nil || got.Type != "hash" || len(got.Pairs) != 2 {
		t.Fatalf("hash %+v %v", got, err)
	}
	if got.TTL <= 0 || got.TTL > time.Hour {
		t.Fatalf("TTL not restored: %v", got.TTL)
	}
	if got, _ := st.GetValue(ctx, "queue"); len(got.Items) != 2 {
		t.Fatalf("queue %+v", got)
	}
	if got, _ := st.GetValue(ctx, "events"); got.Type != "stream" || len(got.Stream) != 1 {
		t.Fatalf("stream %+v", got)
	}
}

func TestCSVImportOptions(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.CreateKey(ctx, "k", "string", NewKeyValue{String: "old"}, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.csv")
	if err := os.WriteFile(path, []byte("key,type,ttl,value\nk,string,3600,new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, skipped, err := importSessionCSVWithOptions(ctx, st, path, CSVImportOptions{Conflict: "Skip", TTLMode: "Include", Reload: true})
	if err != nil || n != 0 || skipped != 1 {
		t.Fatalf("skip %d %d %v", n, skipped, err)
	}
	if v, _ := st.GetValue(ctx, "k"); v.Str != "old" {
		t.Fatalf("skip changed value %q", v.Str)
	}
	n, skipped, err = importSessionCSVWithOptions(ctx, st, path, CSVImportOptions{Conflict: "Overwrite", TTLMode: "Custom", CustomTTL: 2, Reload: false})
	if err != nil || n != 1 || skipped != 0 {
		t.Fatalf("overwrite %d %d %v", n, skipped, err)
	}
	v, err := st.GetValue(ctx, "k")
	if err != nil || v.Str != "new" {
		t.Fatalf("overwrite value %+v %v", v, err)
	}
	if v.TTL <= 0 || v.TTL > 2*time.Second {
		t.Fatalf("custom TTL %v", v.TTL)
	}
}

func TestCSVOverwriteKeepsKeyOnBadRow(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.CreateKey(ctx, "k", "string", NewKeyValue{String: "old"}, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bad.csv")
	if err := os.WriteFile(path, []byte("key,type,ttl,value\nk,json,0,not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, skipped, err := importSessionCSVWithOptions(ctx, st, path, CSVImportOptions{Conflict: "Overwrite", TTLMode: "Include"})
	if err == nil || n != 0 || skipped != 1 {
		t.Fatalf("bad row %d %d %v", n, skipped, err)
	}
	if v, _ := st.GetValue(ctx, "k"); v.Str != "old" {
		t.Fatalf("existing key was deleted: %+v", v)
	}
}

func TestCSVExportWithoutTTL(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.CreateKey(ctx, "k", "string", NewKeyValue{String: "v"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.csv")
	n, _, err := exportSessionCSVWithTTL(ctx, st, path, []string{"k"}, false)
	if err != nil || n != 1 {
		t.Fatalf("export %d %v", n, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "k,string,-1,") {
		t.Fatalf("csv %q", data)
	}
}
