package app

// Value display formats and decoders.

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/elliotchance/phpserialize"
	"github.com/klauspost/compress/zstd"
	"github.com/nlpodyssey/gopickle/pickle"
	pickletypes "github.com/nlpodyssey/gopickle/types"
	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
	"gopkg.in/yaml.v3"
)

var valueFormats = []string{"Auto", "Raw", "JSON", "JSON Unicode", "YAML", "XML", "Hex", "Binary", "BitSet"}
var valueDecodes = []string{"Auto", "None", "Base64", "Base64 URL", "GZip", "Deflate", "Zlib", "ZStd", "LZ4", "Brotli", "Msgpack", "PHP", "Pickle", "Hex", "URL"}

const (
	maxDecoded       = 8 << 20
	maxFormatBytes   = 1 << 20
	maxBitSetBytes   = 64 << 10
	maxCustomStderr  = 4 << 10
	customDecodeTime = 10 * time.Second
)

// formatValue decodes raw and renders it as format. A value that is not
// valid JSON, YAML or XML is shown raw rather than as an error.
func formatValue(raw, format, decode string) (string, error) {
	return formatValueCustom(raw, format, decode, "")
}

func formatValueCustom(raw, format, decode, custom string) (string, error) {
	if format == "Auto" || decode == "Auto" {
		text, _, err := (valueCodec{format: format, decode: decode, decoder: custom}).open(raw)
		return text, err
	}
	b, err := decodeValueCustom([]byte(raw), decode, custom)
	if err != nil {
		return "", err
	}
	if format == "JSON" || format == "JSON Unicode" {
		if err := checkJSONDepth(b); err != nil {
			return "", err
		}
	}
	truncated := false
	note := "… truncated at 1 MiB"
	if format == "BitSet" && len(b) > maxBitSetBytes {
		b, truncated = b[:maxBitSetBytes], true
		note = "… truncated at 64 KiB"
	} else if format != "Raw" && len(b) > maxFormatBytes {
		b, truncated = b[:maxFormatBytes], true
	}
	var out string
	switch format {
	case "JSON":
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, b, "", "  "); err == nil {
			out = pretty.String()
		} else {
			out = string(b)
		}
	case "JSON Unicode":
		var value any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&value); err == nil && json.Valid(b) {
			var pretty bytes.Buffer
			enc := json.NewEncoder(&pretty)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(value); err == nil {
				out = strings.TrimRight(pretty.String(), "\n")
			}
		}
		if out == "" {
			out = string(b)
		}
	case "YAML":
		value, err := decodeYAML(b)
		if errors.Is(err, errDecodeLimit) {
			return "", err
		}
		if err == nil {
			value = normalizeJSON(value)
			if data, err := yaml.Marshal(value); err == nil {
				out = string(data)
			}
		}
		if out == "" {
			out = string(b)
		}
	case "XML":
		out = formatXML(b)
	case "Hex":
		out = hex.EncodeToString(b)
	case "Binary":
		var sb strings.Builder
		sb.Grow(len(b) * 9)
		for i, c := range b {
			if i > 0 {
				sb.WriteByte(' ')
			}
			fmt.Fprintf(&sb, "%08b", c)
		}
		out = sb.String()
	case "BitSet":
		var sb strings.Builder
		sb.Grow(len(b) * 9)
		for i, c := range b {
			if i > 0 {
				sb.WriteByte(' ')
			}
			fmt.Fprintf(&sb, "%08b", c)
		}
		out = sb.String()
	default:
		out = string(b)
	}
	if truncated {
		out += "\n" + note
	}
	if len(out) > maxDecoded {
		return "", errDecodeLimit
	}
	return out, nil
}

func decodeValue(b []byte, decode string) ([]byte, error) {
	return decodeValueCustom(b, decode, "")
}

func decodeValueCustom(b []byte, decode, custom string) ([]byte, error) {
	if len(b) > maxDecoded {
		return nil, errDecodeLimit
	}
	switch decode {
	case "Base64":
		return decodeBase64(strings.TrimSpace(string(b)))
	case "Base64 URL":
		s := strings.TrimSpace(string(b))
		if out, err := base64.RawURLEncoding.DecodeString(s); err == nil {
			return out, nil
		}
		return base64.URLEncoding.DecodeString(s)
	case "Hex":
		return hex.DecodeString(strings.TrimSpace(string(b)))
	case "URL":
		s := string(b)
		if out, err := url.QueryUnescape(s); err == nil {
			return []byte(out), nil
		}
		out, err := url.PathUnescape(s)
		return []byte(out), err
	case "GZip":
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readLimited(r)
	case "Deflate":
		if r, err := zlib.NewReader(bytes.NewReader(b)); err == nil {
			defer r.Close()
			return readLimited(r)
		}
		r := flate.NewReader(bytes.NewReader(b))
		defer r.Close()
		return readLimited(r)
	case "Zlib":
		r, err := zlib.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readLimited(r)
	case "ZStd":
		r, err := zstd.NewReader(bytes.NewReader(b), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxDecoded), zstd.WithDecoderMaxWindow(maxDecoded))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readLimited(r)
	case "LZ4":
		return readLimited(lz4.NewReader(bytes.NewReader(b)))
	case "Brotli":
		return readLimited(brotli.NewReader(bytes.NewReader(b)))
	case "Msgpack":
		return safeDecode(func() ([]byte, error) {
			if err := checkSerialized(b, "Msgpack"); err != nil {
				return nil, err
			}
			var value any
			if err := msgpack.Unmarshal(b, &value); err != nil {
				return nil, err
			}
			return marshalDecoded(normalizeJSON(value))
		})
	case "PHP":
		return safeDecode(func() ([]byte, error) {
			if err := checkSerialized(b, "PHP"); err != nil {
				return nil, err
			}
			value, err := decodePHP(b)
			if err != nil {
				return nil, err
			}
			return marshalDecoded(normalizeJSON(value))
		})
	case "Pickle":
		return safeDecode(func() ([]byte, error) {
			if err := checkPickle(b); err != nil {
				return nil, err
			}
			u := pickle.NewUnpickler(bytes.NewReader(b))
			u.FindClass = func(module, name string) (any, error) {
				return nil, errors.New("Pickle object constructors are not supported")
			}
			value, err := u.Load()
			if err != nil {
				return nil, err
			}
			return marshalDecoded(pickleToJSON(value))
		})
	case "Custom":
		if strings.TrimSpace(custom) == "" {
			return nil, errors.New("custom decoder is not configured")
		}
		return runCustomDecoder(custom, b)
	default:
		return b, nil
	}
}

func safeDecode(fn func() ([]byte, error)) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("decoder failed: %v", r)
		}
	}()
	return fn()
}

func marshalDecoded(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > maxDecoded {
		return nil, errors.New("decoded value is larger than 8 MiB")
	}
	return data, nil
}

func normalizeJSON(v any) any {
	switch x := v.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = normalizeJSON(val)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeJSON(val)
		}
		return out
	case []interface{}:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = normalizeJSON(val)
		}
		return out
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	default:
		return x
	}
}

func decodePHP(b []byte) (any, error) {
	if len(b) == 0 {
		return nil, errors.New("empty PHP serialized value")
	}
	switch b[0] {
	case 'N':
		return nil, nil
	case 'b':
		return phpserialize.UnmarshalBool(b)
	case 'i':
		return phpserialize.UnmarshalInt(b)
	case 'd':
		return phpserialize.UnmarshalFloat(b)
	case 's':
		return phpserialize.UnmarshalString(b)
	case 'a', 'O':
		m, err := phpserialize.UnmarshalAssociativeArray(b)
		if err != nil {
			return nil, err
		}
		return phpArray(m), nil
	default:
		return nil, fmt.Errorf("unsupported PHP serialize type %q", b[0])
	}
}

// phpArray turns a sequential integer-keyed PHP array into a JSON array;
// mixed/string keys stay an object.
func phpArray(m map[interface{}]interface{}) any {
	max := -1
	allInts := true
	for k := range m {
		n, ok := k.(int64)
		if !ok || n < 0 || n > 1<<20 {
			allInts = false
			break
		}
		if int(n) > max {
			max = int(n)
		}
	}
	if allInts && len(m) == max+1 {
		out := make([]any, max+1)
		for k, v := range m {
			out[int(k.(int64))] = normalizeJSON(v)
		}
		return out
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[fmt.Sprint(k)] = normalizeJSON(v)
	}
	return out
}

type pickleBudget struct{ remaining, nodes int }

func pickleToJSON(v any) any {
	budget := pickleBudget{remaining: maxDecoded, nodes: 100000}
	return budget.convert(v, 0)
}

func (budget *pickleBudget) convert(v any, depth int) any {
	budget.nodes--
	budget.remaining -= 16 + depth*4
	if budget.nodes < 0 || budget.remaining < 0 || depth > 64 {
		panic(errDecodeLimit)
	}
	if text, ok := v.(string); ok {
		budget.remaining -= len(text) * 6
		if budget.remaining < 0 {
			panic(errDecodeLimit)
		}
	}
	switch x := v.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return x
	case pickletypes.Dict:
		return pickleDict(x, budget, depth)
	case *pickletypes.Dict:
		if x != nil {
			return pickleDict(*x, budget, depth)
		}
	case pickletypes.List:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = budget.convert(item, depth+1)
		}
		return out
	case *pickletypes.List:
		if x != nil {
			return budget.convert(pickletypes.List(*x), depth+1)
		}
	case pickletypes.Tuple:
		return budget.convert(pickletypes.List(x), depth+1)
	case *pickletypes.Tuple:
		if x != nil {
			return budget.convert(pickletypes.List(*x), depth+1)
		}
	case pickletypes.Set:
		return pickleSetKeys(x, budget, depth)
	case *pickletypes.Set:
		if x != nil {
			return pickleSetKeys(*x, budget, depth)
		}
	case pickletypes.FrozenSet:
		return pickleSetKeys(x, budget, depth)
	case pickletypes.ByteArray:
		budget.remaining -= base64.StdEncoding.EncodedLen(len(x))
		if budget.remaining < 0 {
			panic(errDecodeLimit)
		}
		return base64.StdEncoding.EncodeToString(x)
	case *pickletypes.ByteArray:
		if x != nil {
			return budget.convert(pickletypes.ByteArray(*x), depth+1)
		}
	case *pickletypes.OrderedDict:
		out := make(map[string]any, len(x.Map))
		if x.List != nil {
			for e := x.List.Front(); e != nil; e = e.Next() {
				entry := e.Value.(*pickletypes.OrderedDictEntry)
				out[fmt.Sprint(budget.convert(entry.Key, depth+1))] = budget.convert(entry.Value, depth+1)
			}
		} else {
			for k, entry := range x.Map {
				out[fmt.Sprint(budget.convert(k, depth+1))] = budget.convert(entry.Value, depth+1)
			}
		}
		return out
	case *pickletypes.GenericObject:
		class := ""
		if x.Class != nil {
			class = x.Class.Name
		}
		return map[string]any{
			"__class__": class,
			"args":      budget.convert(x.ConstructorArgs, depth+1),
		}
	default:
		panic(errors.New("unsupported Pickle object"))
	}
	return nil
}

func pickleDict(d pickletypes.Dict, budget *pickleBudget, depth int) map[string]any {
	out := make(map[string]any, len(d))
	for _, entry := range d {
		out[fmt.Sprint(budget.convert(entry.Key, depth+1))] = budget.convert(entry.Value, depth+1)
	}
	return out
}

func pickleSetKeys[T any](m map[interface{}]T, budget *pickleBudget, depth int) []any {
	out := make([]any, 0, len(m))
	for k := range m {
		out = append(out, budget.convert(k, depth+1))
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out
}

func runCustomDecoder(command string, raw []byte) ([]byte, error) {
	return runCustomDecoderContext(context.Background(), command, raw)
}

var customWorkers = make(chan struct{}, 2)

func runCustomDecoderContext(ctx context.Context, command string, raw []byte) ([]byte, error) {
	if len(raw) > maxDecoded {
		return nil, errors.New("value is larger than 8 MiB")
	}
	args, err := parseCommandLine(command)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("custom decoder command is empty")
	}
	for _, arg := range args {
		if strings.Contains(arg, "{VALUE}") {
			return nil, errors.New("use stdin instead of {VALUE}; command arguments can expose Redis values to other processes")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, customDecodeTime)
	defer cancel()
	select {
	case customWorkers <- struct{}{}:
		defer func() { <-customWorkers }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cleanup := manageCustomProcess(cmd)
	defer cleanup()
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Stdin = bytes.NewReader(raw)
	var stdout limitedWriter
	stdout.max, stdout.onLimit = maxDecoded, cancel
	var stderr limitedWriter
	stderr.max = maxCustomStderr
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if stdout.overflow {
		return nil, errors.New("custom decoder output is larger than 8 MiB")
	}
	if ctx.Err() == context.DeadlineExceeded {
		return nil, errors.New("custom decoder timed out")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.buf.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.buf.Bytes(), nil
}

type limitedWriter struct {
	buf      bytes.Buffer
	max      int
	overflow bool
	onLimit  func()
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.max {
		room := w.max - w.buf.Len()
		if room > 0 {
			w.buf.Write(p[:room])
		}
		if !w.overflow && w.onLimit != nil {
			w.onLimit()
		}
		w.overflow = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *limitedWriter) WriteString(s string) (int, error) {
	n := len(s)
	if n > w.max-w.buf.Len() {
		s = s[:w.max-w.buf.Len()]
		if !w.overflow && w.onLimit != nil {
			w.onLimit()
		}
		w.overflow = true
	}
	w.buf.WriteString(s)
	return n, nil
}

func (w *limitedWriter) WriteByte(b byte) error {
	_, err := w.Write([]byte{b})
	return err
}

func decodeBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	var err error
	for _, enc := range encodings {
		var out []byte
		out, err = enc.DecodeString(s)
		if err == nil {
			return out, nil
		}
	}
	return nil, err
}

func readLimited(r io.Reader) ([]byte, error) {
	out, err := io.ReadAll(io.LimitReader(r, maxDecoded+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxDecoded {
		return nil, errors.New("decoded value is larger than 8 MiB")
	}
	return out, nil
}

func formatXML(b []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	out := limitedWriter{max: maxDecoded}
	enc := xml.NewEncoder(&out)
	enc.Indent("", "  ")
	depth, nodes := 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return string(b)
		}
		nodes++
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
		if depth > 64 || nodes > 100000 || out.overflow {
			return string(b)
		}
		if err := enc.EncodeToken(tok); err != nil {
			return string(b)
		}
	}
	if err := enc.Flush(); err != nil {
		return string(b)
	}
	if out.overflow {
		return string(b)
	}
	return out.buf.String()
}

// cellValueCustom formats a value for one table cell, flattening its lines.
func cellValueCustom(raw, format, decode, custom string) string {
	s, err := formatValueCustom(raw, format, decode, custom)
	if err != nil {
		return "⚠ " + err.Error()
	}
	return strings.ReplaceAll(s, "\n", " ")
}
