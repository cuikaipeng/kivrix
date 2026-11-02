package app

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
)

type keyFilter struct{ query, mode, typ string }

func (s *session) keyFilter() keyFilter { return keyFilter{s.search, s.keyMatch, s.keyType} }

func (s *Store) scanFiltered(ctx context.Context, cursor string, f keyFilter, count int64) ([]string, string, error) {
	typ := f.typ
	if typ == "All types" {
		typ = ""
	}
	if typ == "json" {
		typ = "ReJSON-RL"
	}
	if f.mode == "Exact" && f.query != "" {
		actual, err := s.client.Type(ctx, f.query).Result()
		if err != nil {
			return nil, "", err
		}
		if actual != "none" && (typ == "" || actual == typ) {
			return []string{f.query}, "", nil
		}
		return nil, "", nil
	}
	pattern := f.query
	if f.mode != "Pattern" {
		pattern = scanPattern(f.query)
	}
	return s.ScanPage(ctx, cursor, pattern, count, typ)
}

func (s *session) scanSize() int64 {
	if s.cfg.ScanSize > 0 {
		return int64(s.cfg.ScanSize)
	}
	if s.app.prefs.ScanSize > 0 {
		return int64(s.app.prefs.ScanSize)
	}
	return scanPageSize
}

func (s *session) valuePageSize() int {
	if s.app.prefs.ValuePageSize > 0 {
		return int(s.app.prefs.ValuePageSize)
	}
	return valuePageSize
}

func (s *session) setKeyView() {
	delim := s.cfg.Separator
	if delim == "" {
		delim = keyTreeDelim
	}
	if s.keyView == "List" {
		delim = ""
	}
	tree := newKeyTree(delim)
	for key, n := range s.keys.byPath {
		if n.isKey {
			tree.add(key)
		}
	}
	s.keys = tree
}

func (a *App) viewKeyFilters(c *ui.Context, pal *palette, s *session) {
	ui.Row(c).Gap(8).Wrap().Children(func() {
		if a.selectOption(c, &s.keyMatch, []string{"Contains", "Pattern", "Exact"}, false).
			Height(30).Padding(0, 7).Width(86).MinWidth(86).Grow(1).Label(a.tr("Key match")).Changed() {
			s.startScan()
		}
		if a.selectOption(c, &s.keyType, []string{"All types", "string", "hash", "list", "set", "zset", "stream", "json"}, false).
			Height(30).Padding(0, 7).Width(88).MinWidth(88).Grow(1).Label(a.tr("Key type")).Changed() {
			s.startScan()
		}
		if a.selectOption(c, &s.keyView, []string{"Tree", "List"}, false).
			Height(30).Padding(0, 7).Width(60).MinWidth(60).Grow(1).Label(a.tr("Key view")).Changed() {
			s.setKeyView()
		}
	})
}

func dbNumber(label string) string {
	n, _, _ := strings.Cut(label, " · ")
	return strings.TrimPrefix(n, "db")
}
