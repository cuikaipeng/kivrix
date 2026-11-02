package app

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
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/elliotchance/phpserialize"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
)

// A codec is captured with the editor buffer, so changing the selected key
// or preferences cannot change how an in-flight save encodes its contents.
type valueCodec struct {
	format, decode, decoder, encoder string
}

func (s *session) valueCodec() valueCodec {
	decode, command := s.decodeCommand()
	d, _ := s.currentCustomDecoder()
	return valueCodec{s.valFormat, decode, command, d.EncodeCommand}
}

func (c valueCodec) editable() bool {
	return c.decode != "Custom" || strings.TrimSpace(c.encoder) != ""
}

func (a *App) prepareRowValue(s *session) {
	if a.rowCancel != nil {
		a.rowCancel()
	}
	a.rowGeneration++
	id, codec, r := a.rowGeneration, s.valueCodec(), a.row
	if r.typ == "stream" {
		return
	}
	raw := r.value
	if r.typ == "set" || r.typ == "zset" {
		raw = r.member
	}
	a.row.loading = true
	ctx, cancel := context.WithCancel(context.Background())
	a.rowCancel = cancel
	go func() {
		defer cancel()
		text, resolved, err := codec.openContext(ctx, raw)
		if err == nil && !codec.editable() {
			err = errors.New("choose a decoder with an encoder to edit")
		}
		a.update(func() {
			if !a.rowOpen || a.rowGeneration != id || s.closed {
				return
			}
			a.row.loading = false
			if err != nil {
				a.row.err = err.Error()
				return
			}
			a.row.codec = resolved
			if r.typ == "set" || r.typ == "zset" {
				a.row.member = text
			} else {
				a.row.value = text
			}
		})
	}()
}

func (c valueCodec) open(raw string) (string, valueCodec, error) {
	return c.openContext(context.Background(), raw)
}

var codecWorkers = make(chan struct{}, 2)

func (c valueCodec) openContext(ctx context.Context, raw string) (string, valueCodec, error) {
	select {
	case codecWorkers <- struct{}{}:
		defer func() { <-codecWorkers }()
	case <-ctx.Done():
		return "", c, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", c, err
	}
	if len(raw) > maxDecoded {
		return "", c, errDecodeLimit
	}
	if c.decode == "Auto" {
		c.decode = detectDecode(raw)
	}
	var b []byte
	var err error
	if c.decode == "Custom" {
		b, err = runCustomDecoderContext(ctx, c.decoder, []byte(raw))
	} else {
		b, err = decodeValueCustom([]byte(raw), c.decode, c.decoder)
	}
	if err != nil {
		return "", c, err
	}
	if c.format == "Auto" {
		c.format = "Raw"
		if json.Valid(b) {
			c.format = "JSON"
		} else if strings.HasPrefix(strings.TrimSpace(string(b)), "<") {
			c.format = "XML"
		} else if !utf8.Valid(b) {
			c.format = "Hex"
		}
	}
	// The display formatter truncates previews. An editable buffer must never
	// contain that truncation marker, since saving it would destroy data.
	limit := maxDecoded
	if c.format != "Raw" && c.format != "" {
		limit = maxFormatBytes
	}
	if c.format == "BitSet" {
		limit = maxBitSetBytes
	}
	if len(b) > limit {
		return "", c, fmt.Errorf("value exceeds the %d-byte editor limit; use Raw or export it", limit)
	}
	if (c.format == "Raw" || c.format == "") && !utf8.Valid(b) {
		return "", c, errors.New("binary value: choose Hex or Binary to edit")
	}
	text, err := formatValue(string(b), c.format, "None")
	if len(text) > maxEditorBytes {
		return "", c, errValueLimit
	}
	return text, c, err
}

func (c valueCodec) save(text string) ([]byte, error) {
	if len(text) > maxDecoded {
		return nil, errDecodeLimit
	}
	if !c.editable() {
		return nil, errors.New("custom encoder is not configured")
	}
	b := []byte(text)
	switch c.format {
	case "Hex":
		var err error
		b, err = hex.DecodeString(strings.Join(strings.Fields(text), ""))
		if err != nil {
			return nil, err
		}
	case "Binary", "BitSet":
		bits := strings.Join(strings.Fields(text), "")
		if len(bits)%8 != 0 {
			return nil, errors.New("binary values need eight bits per byte")
		}
		b = make([]byte, len(bits)/8)
		for i := range b {
			n, err := strconv.ParseUint(bits[i*8:i*8+8], 2, 8)
			if err != nil {
				return nil, err
			}
			b[i] = byte(n)
		}
	case "JSON", "JSON Unicode":
		if err := checkJSONDepth(b); err != nil {
			return nil, err
		}
		var out bytes.Buffer
		if err := json.Compact(&out, b); err != nil {
			return nil, err
		}
		b = out.Bytes()
	case "YAML":
		if _, err := decodeYAML(b); err != nil {
			return nil, err
		}
	case "XML":
		d := xml.NewDecoder(bytes.NewReader(b))
		for {
			_, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return encodeValue(b, c.decode, c.encoder)
}

func encodeValue(b []byte, decode, command string) ([]byte, error) {
	switch decode {
	case "", "None", "Auto":
		return b, nil
	case "Base64":
		return []byte(base64.StdEncoding.EncodeToString(b)), nil
	case "Base64 URL":
		return []byte(base64.URLEncoding.EncodeToString(b)), nil
	case "Hex":
		return []byte(hex.EncodeToString(b)), nil
	case "URL":
		return []byte(url.QueryEscape(string(b))), nil
	case "Custom":
		return runCustomDecoder(command, b)
	case "Msgpack", "PHP":
		if err := checkJSONDepth(b); err != nil {
			return nil, err
		}
		if !json.Valid(b) {
			return nil, fmt.Errorf("%s editing requires JSON", decode)
		}
		var v any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		v, err := codecNumbers(v, decode == "Msgpack")
		if err != nil {
			return nil, err
		}
		if decode == "Msgpack" {
			return msgpack.Marshal(v)
		}
		return phpserialize.Marshal(v, nil)
	case "Pickle":
		if !json.Valid(b) {
			return nil, errors.New("Pickle editing requires JSON")
		}
		for _, python := range []string{"python3", "python"} {
			if _, err := exec.LookPath(python); err == nil {
				return runCustomDecoder(python+` -c 'import json,pickle,sys; sys.stdout.buffer.write(pickle.dumps(json.load(sys.stdin)))'`, b)
			}
		}
		return nil, errors.New("Python is required to encode Pickle values")
	}
	var out bytes.Buffer
	var w io.WriteCloser
	var err error
	switch decode {
	case "GZip":
		w = gzip.NewWriter(&out)
	case "Deflate":
		w, err = flate.NewWriter(&out, flate.DefaultCompression)
	case "Zlib":
		w = zlib.NewWriter(&out)
	case "ZStd":
		w, err = zstd.NewWriter(&out)
	case "LZ4":
		w = lz4.NewWriter(&out)
	case "Brotli":
		w = brotli.NewWriter(&out)
	default:
		return nil, fmt.Errorf("unsupported encoder %q", decode)
	}
	if err != nil {
		return nil, err
	}
	if _, err = w.Write(b); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func codecNumbers(v any, unsigned bool) (any, error) {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, nil
		}
		if !strings.ContainsAny(string(x), ".eE") {
			if n, err := strconv.ParseUint(string(x), 10, 64); unsigned && err == nil {
				return n, nil
			}
			return nil, fmt.Errorf("integer %s is outside the encoder's range", x)
		}
		return x.Float64()
	case []any:
		for i := range x {
			var err error
			x[i], err = codecNumbers(x[i], unsigned)
			if err != nil {
				return nil, err
			}
		}
	case map[string]any:
		for k := range x {
			var err error
			x[k], err = codecNumbers(x[k], unsigned)
			if err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

// Prefer recognizable binary signatures. Text candidates must decode into
// text, so ordinary values such as "1234" are not mistaken for Base64.
func detectDecode(raw string) string {
	if len(raw) > maxDecoded {
		return "None"
	}
	b := []byte(raw)
	candidates := []string{}
	switch {
	case bytes.HasPrefix(b, []byte{0x1f, 0x8b}):
		candidates = append(candidates, "GZip")
	case bytes.HasPrefix(b, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		candidates = append(candidates, "ZStd")
	case bytes.HasPrefix(b, []byte{4, 0x22, 0x4d, 0x18}):
		candidates = append(candidates, "LZ4")
	case len(b) > 1 && b[0] == 0x78:
		candidates = append(candidates, "Zlib")
	case len(b) > 1 && b[0] == 0x80:
		candidates = append(candidates, "Pickle")
	case strings.HasPrefix(raw, "a:") || strings.HasPrefix(raw, "s:") || strings.HasPrefix(raw, "O:"):
		candidates = append(candidates, "PHP")
	}
	if !utf8.Valid(b) {
		candidates = append(candidates, "Msgpack", "Deflate", "Brotli")
	}
	if len(raw) >= 4 && len(raw)%4 == 0 {
		candidates = append(candidates, "Base64")
	}
	for _, name := range candidates {
		decoded, err := decodeValue(b, name)
		if err != nil || len(decoded) == 0 {
			continue
		}
		if name != "Base64" {
			return name
		}
		if utf8.Valid(decoded) && strings.IndexFunc(string(decoded), func(r rune) bool { return !unicode.IsPrint(r) && !unicode.IsSpace(r) }) < 0 {
			return name
		}
	}
	return "None"
}

// Custom programs run off the UI thread and at most two at a time. A changed
// key/codec cancels queued work; obsolete completions never enter the cache.
func (s *session) formattedCellValue(raw string) string {
	codec := s.valueCodec()
	if s.cellCache == nil || s.cellCodec != codec || s.cellKey != s.valID {
		if s.cellCancel != nil {
			s.cellCancel()
		}
		s.cellContext, s.cellCancel = context.WithCancel(context.Background())
		s.cellKey, s.cellCodec = s.valID, codec
		s.cellCache, s.cellPending = map[string]string{}, map[string]bool{}
		if s.cellWorkers == nil {
			s.cellWorkers = make(chan struct{}, 2)
		}
	}
	if text, ok := s.cellCache[raw]; ok {
		return text
	}
	if s.cellPending[raw] {
		return "…"
	}
	// Render only a preview; opening a row uses the complete, bounded value.
	if len(raw) > maxFormatBytes {
		return limitedText(raw, maxLiveText)
	}
	if len(s.cellCache) >= 512 {
		s.cellCache = map[string]string{}
	}
	if len(s.cellPending) >= 32 {
		return "…"
	}
	s.cellPending[raw] = true
	ctx, id, workers := s.cellContext, s.valID, s.cellWorkers
	go func() {
		select {
		case workers <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-workers }()
		if ctx.Err() != nil {
			return
		}
		var text string
		var err error
		if codec.decode == "Custom" {
			var out []byte
			out, err = runCustomDecoderContext(ctx, codec.decoder, []byte(raw))
			if err == nil {
				text, err = formatValue(string(out), codec.format, "None")
			}
		} else {
			text, err = formatValueCustom(raw, codec.format, codec.decode, "")
		}
		if err != nil {
			text = "⚠ " + err.Error()
		}
		text = strings.ReplaceAll(limitedText(text, maxLiveText), "\n", " ")
		s.app.update(func() {
			if s.closed || ctx.Err() != nil || s.valID != id || s.cellCodec != codec {
				return
			}
			s.cellCache[raw] = text
			delete(s.cellPending, raw)
		})
	}()
	return "…"
}
