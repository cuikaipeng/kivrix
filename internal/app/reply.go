package app

// CLI command parsing and Redis reply formatting.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const maxCLIResult = 64 << 10

// parseCommandLine splits a command line with single and double quotes.
func parseCommandLine(line string) ([]string, error) {
	var args []string
	var b strings.Builder
	var quote rune
	escaped, has := false, false
	for _, r := range line {
		if escaped {
			b.WriteRune(r)
			escaped, has = false, true
			continue
		}
		if quote != 0 {
			switch r {
			case '\\':
				if quote == '"' {
					escaped = true
				} else {
					b.WriteRune(r)
				}
			case quote:
				quote = 0
			default:
				b.WriteRune(r)
			}
			has = true
			continue
		}
		switch {
		case r == '\\':
			escaped, has = true, true
		case r == '\'' || r == '"':
			quote, has = r, true
		case unicode.IsSpace(r):
			if has {
				args = append(args, b.String())
				b.Reset()
				has = false
			}
		default:
			b.WriteRune(r)
			has = true
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, errors.New("unclosed quote")
	}
	if has {
		args = append(args, b.String())
	}
	return args, nil
}

// splitList splits channel lists on commas and whitespace.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == ','
	})
}

// formatReply renders a go-redis reply for the CLI.
func formatReply(v any) string {
	b := limitedWriter{max: maxCLIResult}
	writeReply(&b, v, 0)
	out := strings.TrimRight(b.buf.String(), "\n")
	if out == "" {
		return "(empty)"
	}
	if b.overflow {
		out += "\n… truncated"
	}
	return out
}

func writeReply(b *limitedWriter, v any, depth int) {
	if b.overflow || depth > 64 {
		b.overflow = true
		return
	}
	switch x := v.(type) {
	case nil:
		b.WriteString("(nil)")
	case string:
		b.WriteString(x)
	case []byte:
		b.Write(x)
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int:
		b.WriteString(strconv.Itoa(x))
	case float64:
		b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case error:
		b.WriteString(x.Error())
	case []any:
		if len(x) == 0 {
			b.WriteString("(empty list or set)")
			return
		}
		for i, item := range x {
			if b.overflow {
				break
			}
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.Repeat(" ", depth))
			fmt.Fprintf(b, "%d) ", i+1)
			writeReply(b, item, depth+3)
		}
	case []string:
		for i, item := range x {
			if b.overflow {
				break
			}
			if i > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(b, "%s%d) ", strings.Repeat(" ", depth), i+1)
			writeReply(b, item, depth+3)
		}
	case map[string]string:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if b.overflow {
				break
			}
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.Repeat(" ", depth))
			b.WriteString(k)
			b.WriteString(" = ")
			writeReply(b, x[k], depth)
		}
	case map[any]any:
		for key, value := range x {
			if b.overflow {
				break
			}
			writeReply(b, key, depth+1)
			b.WriteString(" = ")
			writeReply(b, value, depth+1)
			b.WriteByte('\n')
		}
	case map[string]any:
		for key, value := range x {
			if b.overflow {
				break
			}
			b.WriteString(key)
			b.WriteString(" = ")
			writeReply(b, value, depth+1)
			b.WriteByte('\n')
		}
	default:
		b.WriteString(fmt.Sprint(x))
	}
}
