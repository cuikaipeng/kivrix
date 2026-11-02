package app

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
)

func TestFormatValue(t *testing.T) {
	if got, err := formatValue(`{"a":1}`, "JSON", "None"); err != nil || !strings.Contains(got, "\n  \"a\"") {
		t.Fatalf("json %q %v", got, err)
	}
	if got, err := formatValue("not json", "JSON", "None"); err != nil || got != "not json" {
		t.Fatalf("invalid json %q %v", got, err)
	}
	if got, err := formatValue("hi", "Hex", "None"); err != nil || got != "6869" {
		t.Fatalf("hex %q %v", got, err)
	}
	if got, err := formatValue("A", "Binary", "None"); err != nil || got != "01000001" {
		t.Fatalf("binary %q %v", got, err)
	}
	if got, err := formatValue("<a><b>x</b></a>", "XML", "None"); err != nil || !strings.Contains(got, "\n  <b>") {
		t.Fatalf("xml %q %v", got, err)
	}
}

func TestDecodeValue(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("hello"))
	if got, err := formatValue(b64, "Raw", "Base64"); err != nil || got != "hello" {
		t.Fatalf("base64 %q %v", got, err)
	}

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write([]byte("gzipped"))
	w.Close()
	if got, err := formatValue(gz.String(), "Raw", "GZip"); err != nil || got != "gzipped" {
		t.Fatalf("gzip %q %v", got, err)
	}

	var zl bytes.Buffer
	zw := zlib.NewWriter(&zl)
	zw.Write([]byte("deflated"))
	zw.Close()
	if got, err := formatValue(zl.String(), "Raw", "Deflate"); err != nil || got != "deflated" {
		t.Fatalf("deflate %q %v", got, err)
	}

	if _, err := formatValue("not base64!", "Raw", "Base64"); err == nil {
		t.Fatal("bad base64 accepted")
	}
}

func TestMoreFormatsAndDecoders(t *testing.T) {
	if got, err := formatValue(`{"name":"\u4f60\u597d"}`, "JSON Unicode", "None"); err != nil || !strings.Contains(got, "你好") {
		t.Fatalf("unicode json %q %v", got, err)
	}
	if got, err := formatValue("aGVsbG8", "Raw", "Base64 URL"); err != nil || got != "hello" {
		t.Fatalf("base64 url %q %v", got, err)
	}
	if got, err := formatValue("68656c6c6f", "Raw", "Hex"); err != nil || got != "hello" {
		t.Fatalf("hex %q %v", got, err)
	}
	if got, err := formatValue("hello%20world", "Raw", "URL"); err != nil || got != "hello world" {
		t.Fatalf("url %q %v", got, err)
	}
}

func TestMoreFormats(t *testing.T) {
	if got, err := formatValue(`{"a":1,"b":2}`, "YAML", "None"); err != nil || !strings.Contains(got, "a: 1") {
		t.Fatalf("yaml %q %v", got, err)
	}
	if got, err := formatValue("A", "BitSet", "None"); err != nil || got != "01000001" {
		t.Fatalf("bitset %q %v", got, err)
	}
}

func TestSerializationDecoders(t *testing.T) {
	data, err := msgpack.Marshal(map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := formatValue(string(data), "JSON", "Msgpack"); err != nil || !strings.Contains(got, `"a": 1`) {
		t.Fatalf("msgpack %q %v", got, err)
	}
	if got, err := formatValue(`a:1:{s:1:"a";i:1;}`, "JSON", "PHP"); err != nil || !strings.Contains(got, `"a": 1`) {
		t.Fatalf("php %q %v", got, err)
	}
	if got, err := formatValue("S'hello'\np0\n.", "Raw", "Pickle"); err != nil || got != `"hello"` {
		t.Fatalf("pickle %q %v", got, err)
	}
}

func TestCompressionDecoders(t *testing.T) {
	var zbuf bytes.Buffer
	zw, err := zstd.NewWriter(&zbuf)
	if err != nil {
		t.Fatal(err)
	}
	zw.Write([]byte("zstd"))
	zw.Close()
	if got, err := formatValue(zbuf.String(), "Raw", "ZStd"); err != nil || got != "zstd" {
		t.Fatalf("zstd %q %v", got, err)
	}

	var lbuf bytes.Buffer
	lw := lz4.NewWriter(&lbuf)
	lw.Write([]byte("lz4"))
	lw.Close()
	if got, err := formatValue(lbuf.String(), "Raw", "LZ4"); err != nil || got != "lz4" {
		t.Fatalf("lz4 %q %v", got, err)
	}

	var bbuf bytes.Buffer
	bw := brotli.NewWriter(&bbuf)
	bw.Write([]byte("brotli"))
	bw.Close()
	if got, err := formatValue(bbuf.String(), "Raw", "Brotli"); err != nil || got != "brotli" {
		t.Fatalf("brotli %q %v", got, err)
	}
}

func TestCustomDecoder(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat is not available")
	}
	if got, err := formatValueCustom("hello", "Raw", "Custom", "cat"); err != nil || got != "hello" {
		t.Fatalf("custom %q %v", got, err)
	}
	if _, err := formatValue("hello", "Raw", "Custom"); err == nil {
		t.Fatal("unconfigured custom decoder did not fail")
	}
}
