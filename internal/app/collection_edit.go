package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Convert the complete batch before writing anything, so an invalid value
// cannot leave half a batch on the server.
func (s *Store) addRows(ctx context.Context, r rowEdit) error {
	v, err := parseNewValue(r.typ, r.bulkText)
	if err != nil {
		return err
	}
	encode := func(text string) (string, error) {
		b, err := r.codec.save(text)
		return string(b), err
	}
	switch r.typ {
	case "hash":
		for field, value := range v.Fields {
			v.Fields[field], err = encode(value)
			if err != nil {
				return fmt.Errorf("%s: %w", field, err)
			}
		}
		return s.client.HSet(ctx, r.key, v.Fields).Err()
	case "list", "set":
		args := make([]any, len(v.Items))
		for i, value := range v.Items {
			args[i], err = encode(value)
			if err != nil {
				return err
			}
		}
		if r.typ == "set" {
			return s.client.SAdd(ctx, r.key, args...).Err()
		}
		if r.head {
			slices.Reverse(args)
			return s.client.LPush(ctx, r.key, args...).Err()
		}
		return s.client.RPush(ctx, r.key, args...).Err()
	case "zset":
		values := make([]redis.Z, 0, len(v.Scores))
		for member, score := range v.Scores {
			encoded, err := encode(member)
			if err != nil {
				return err
			}
			values = append(values, redis.Z{Member: encoded, Score: score})
		}
		return s.client.ZAdd(ctx, r.key, values...).Err()
	}
	return fmt.Errorf("cannot add rows to %s", r.typ)
}

func (s *session) sortHashRows() {
	if s.val.Type != "hash" || s.valSort.Column == "" {
		return
	}
	selected := ""
	hasSelection := s.valRowSel >= 0 && s.valRowSel < len(s.val.Pairs)
	if hasSelection {
		selected = s.val.Pairs[s.valRowSel][0]
	}
	column := 0
	if s.valSort.Column == "value" {
		column = 1
	}
	sort.SliceStable(s.val.Pairs, func(i, j int) bool {
		order := strings.Compare(s.val.Pairs[i][column], s.val.Pairs[j][column])
		if s.valSort.Descending {
			return order > 0
		}
		return order < 0
	})
	if hasSelection {
		for i, row := range s.val.Pairs {
			if row[0] == selected {
				s.valRowSel = i
				break
			}
		}
	}
	s.lastValSort = s.valSort
}
