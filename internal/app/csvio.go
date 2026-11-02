package app

// CSV import and export. Values are kept as JSON in the value column:
// objects for hashes, arrays for lists/sets, member/score objects for
// sorted sets, and id/fields objects for streams.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var csvHeader = []string{"key", "type", "ttl", "value"}

type csvZSet struct {
	Member string  `json:"member"`
	Score  float64 `json:"score"`
}

type csvStream struct {
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

func encodeCSVValue(v Value) (string, error) {
	// JSON replaces invalid UTF-8. Refuse that conversion instead of exporting
	// a collection that could silently change data on import.
	valid := func(values ...string) bool {
		for _, value := range values {
			if !utf8.ValidString(value) {
				return false
			}
		}
		return true
	}
	for _, p := range v.Pairs {
		if !valid(p[0], p[1]) {
			return "", errors.New("CSV collections require UTF-8 fields and values")
		}
	}
	if !valid(v.Items...) {
		return "", errors.New("CSV collections require UTF-8 fields and values")
	}
	for _, entry := range v.Stream {
		for _, field := range entry.Fields {
			if !valid(field[0], field[1]) {
				return "", errors.New("CSV collections require UTF-8 fields and values")
			}
		}
	}
	switch v.Type {
	case "string", "json":
		return v.Str, nil
	case "hash":
		m := make(map[string]string, len(v.Pairs))
		for _, p := range v.Pairs {
			m[p[0]] = p[1]
		}
		b, err := json.Marshal(m)
		return string(b), err
	case "list", "set":
		b, err := json.Marshal(v.Items)
		return string(b), err
	case "zset":
		out := make([]csvZSet, 0, len(v.Pairs))
		for _, p := range v.Pairs {
			score, _ := strconv.ParseFloat(p[1], 64)
			out = append(out, csvZSet{Member: p[0], Score: score})
		}
		b, err := json.Marshal(out)
		return string(b), err
	case "stream":
		out := make([]csvStream, 0, len(v.Stream))
		for _, e := range v.Stream {
			fields := make(map[string]string, len(e.Fields))
			for _, f := range e.Fields {
				fields[f[0]] = f[1]
			}
			out = append(out, csvStream{ID: e.ID, Fields: fields})
		}
		b, err := json.Marshal(out)
		return string(b), err
	default:
		return "", fmt.Errorf("type %q cannot be exported", v.Type)
	}
}

func decodeCSVValue(typ, value string) (NewKeyValue, error) {
	switch typ {
	case "string":
		return NewKeyValue{String: value}, nil
	case "json":
		if !json.Valid([]byte(value)) {
			return NewKeyValue{}, errors.New("value is not valid JSON")
		}
		return NewKeyValue{String: value}, nil
	case "hash":
		var m map[string]any
		if err := json.Unmarshal([]byte(value), &m); err != nil {
			return NewKeyValue{}, fmt.Errorf("hash value: %w", err)
		}
		fields := make(map[string]string, len(m))
		for k, v := range m {
			fields[k] = fmt.Sprint(v)
		}
		return NewKeyValue{Fields: fields}, nil
	case "list", "set":
		var raw []any
		if err := json.Unmarshal([]byte(value), &raw); err != nil {
			return NewKeyValue{}, fmt.Errorf("%s value: %w", typ, err)
		}
		items := make([]string, len(raw))
		for i, v := range raw {
			items[i] = fmt.Sprint(v)
		}
		return NewKeyValue{Items: items}, nil
	case "zset":
		var raw []csvZSet
		if err := json.Unmarshal([]byte(value), &raw); err != nil {
			return NewKeyValue{}, fmt.Errorf("zset value: %w", err)
		}
		scores := make(map[string]float64, len(raw))
		for _, z := range raw {
			scores[z.Member] = z.Score
		}
		return NewKeyValue{Scores: scores}, nil
	case "stream":
		var raw []csvStream
		if err := json.Unmarshal([]byte(value), &raw); err != nil {
			return NewKeyValue{}, fmt.Errorf("stream value: %w", err)
		}
		out := make([]StreamEntry, 0, len(raw))
		for _, e := range raw {
			entry := StreamEntry{ID: e.ID}
			for field, v := range e.Fields {
				entry.Fields = append(entry.Fields, [2]string{field, v})
			}
			out = append(out, entry)
		}
		return NewKeyValue{Stream: out}, nil
	default:
		return NewKeyValue{}, fmt.Errorf("unsupported type %q", typ)
	}
}

// exportSessionCSVWithTTL writes keys as CSV and reports how many were skipped.
func exportSessionCSVWithTTL(ctx context.Context, st *Store, path string, keys []string, includeTTL bool, progress ...func(int, int)) (int, int, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, 0, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return 0, 0, err
	}
	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		f.Close()
		return 0, 0, err
	}
	var exported, skipped int
	report := func() {
		for _, fn := range progress {
			fn(exported, skipped)
		}
	}
	defer report()
	var firstErr error
	for _, key := range keys {
		report()
		if err := ctx.Err(); err != nil {
			w.Flush()
			f.Close()
			return exported, skipped, err
		}
		v, err := st.ExportValue(ctx, key)
		if err == nil {
			var value string
			value, err = encodeCSVValue(v)
			if err == nil {
				ttl := int64(-1)
				if includeTTL && v.TTL >= 0 {
					ttl = int64(v.TTL / time.Second)
				}
				err = w.Write([]string{key, v.Type, strconv.FormatInt(ttl, 10), value})
			}
		}
		if err != nil {
			skipped++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", key, err)
			}
			continue
		}
		exported++
	}
	w.Flush()
	if err := w.Error(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := f.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return exported, skipped, firstErr
}

// CSVImportOptions controls conflicts and TTLs while importing.
type CSVImportOptions struct {
	Conflict  string // "skip" or "overwrite"
	TTLMode   string // "include", "ignore" or "custom"
	CustomTTL int64
	Reload    bool
}

func defaultCSVImportOptions() CSVImportOptions {
	return CSVImportOptions{Conflict: "Skip", TTLMode: "Include", Reload: true}
}

func (o CSVImportOptions) normalized() CSVImportOptions {
	if strings.EqualFold(o.Conflict, "overwrite") {
		o.Conflict = "overwrite"
	} else {
		o.Conflict = "skip"
	}
	switch strings.ToLower(o.TTLMode) {
	case "ignore", "no ttl":
		o.TTLMode = "ignore"
	case "custom", "custom ttl":
		o.TTLMode = "custom"
	default:
		o.TTLMode = "include"
	}
	if o.CustomTTL < 0 {
		o.CustomTTL = 0
	}
	return o
}

// importSessionCSVWithOptions reads keys from a CSV file, skipping rows that fail.
func importSessionCSVWithOptions(ctx context.Context, st *Store, path string, opts CSVImportOptions, progress ...func(int, int)) (int, int, error) {
	if st.cfg.ReadOnly {
		return 0, 0, errReadOnly
	}
	opts = opts.normalized()
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	// Bound each record, including multi-line quoted fields. The small read-
	// ahead buffer belongs to csv.Reader; no whole-file limit is needed.
	limited := &io.LimitedReader{R: f, N: maxReplyBytes + 4096}
	r := csv.NewReader(limited)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if limited.N <= 0 {
		return 0, 0, errValueLimit
	}
	if err != nil {
		return 0, 0, err
	}
	columns := map[string]int{}
	for i, name := range header {
		name = strings.TrimPrefix(strings.TrimSpace(name), "\ufeff")
		columns[strings.ToLower(name)] = i
	}
	for _, required := range []string{"key", "type", "value"} {
		if _, ok := columns[required]; !ok {
			return 0, 0, fmt.Errorf("CSV is missing the %q column", required)
		}
	}
	var imported, skipped int
	report := func() {
		for _, fn := range progress {
			fn(imported, skipped)
		}
	}
	defer report()
	var firstErr error
	for row := 2; ; row++ {
		report()
		if err := ctx.Err(); err != nil {
			return imported, skipped, err
		}
		limited.N = maxReplyBytes + 4096
		record, err := r.Read()
		if limited.N <= 0 {
			return imported, skipped + 1, fmt.Errorf("row %d: CSV record exceeds 16 MiB", row)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			skipped++
			if firstErr == nil {
				firstErr = fmt.Errorf("row %d: %w", row, err)
			}
			continue
		}
		get := func(name string) string {
			i, ok := columns[name]
			if !ok || i >= len(record) {
				return ""
			}
			return record[i]
		}
		key := get("key")
		typ := strings.ToLower(strings.TrimSpace(get("type")))
		if key == "" || typ == "" {
			skipped++
			if firstErr == nil {
				firstErr = fmt.Errorf("row %d: key and type are required", row)
			}
			continue
		}
		ttl := int64(-1)
		if text := strings.TrimSpace(get("ttl")); text != "" {
			ttl, err = strconv.ParseInt(text, 10, 64)
			if err != nil || ttl < -1 {
				skipped++
				if firstErr == nil {
					firstErr = fmt.Errorf("row %d: bad TTL %q", row, text)
				}
				continue
			}
		}
		switch opts.TTLMode {
		case "ignore":
			ttl = 0
		case "custom":
			ttl = opts.CustomTTL
		}
		v, err := decodeCSVValue(typ, get("value"))
		if err != nil {
			skipped++
			if firstErr == nil {
				firstErr = fmt.Errorf("row %d key %q: %w", row, key, err)
			}
			continue
		}
		if conn := st.client; conn != nil {
			exists, xerr := conn.Exists(ctx, key).Result()
			if xerr != nil {
				skipped++
				if firstErr == nil {
					firstErr = fmt.Errorf("row %d key %q: %w", row, key, xerr)
				}
				continue
			}
			if exists > 0 {
				if opts.Conflict == "skip" {
					skipped++
					continue
				}
				if _, derr := st.DeleteKeys(ctx, key); derr != nil {
					skipped++
					if firstErr == nil {
						firstErr = fmt.Errorf("row %d key %q: %w", row, key, derr)
					}
					continue
				}
			}
		}
		if err := st.CreateKey(ctx, key, typ, v, durationSeconds(ttl)); err != nil {
			skipped++
			if firstErr == nil {
				firstErr = fmt.Errorf("row %d key %q: %w", row, key, err)
			}
			continue
		}
		imported++
	}
	return imported, skipped, firstErr
}

func durationSeconds(ttl int64) time.Duration {
	if ttl <= 0 {
		return 0
	}
	return time.Duration(ttl) * time.Second
}
