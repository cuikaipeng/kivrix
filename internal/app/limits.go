package app

import (
	"errors"
	"io"
	"os"
)

const (
	maxEditorBytes  = 1 << 20
	maxLoadedRows   = 10000
	maxProfileBytes = 4 << 20
)

var errValueLimit = errors.New("value exceeds the display limit (1 MiB of text, 8 MiB of collections or 10000 rows); filter the collection or export the key")

func valueFootprint(v Value) (size, rows int) {
	size = len(v.Str)
	rows = len(v.Items) + len(v.Pairs) + len(v.Stream)
	for _, item := range v.Items {
		size += len(item) + 16
	}
	for _, pair := range v.Pairs {
		size += len(pair[0]) + len(pair[1]) + 32
	}
	for _, entry := range v.Stream {
		size += len(entry.ID) + 32
		for _, pair := range entry.Fields {
			size += len(pair[0]) + len(pair[1]) + 32
		}
	}
	return
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("file exceeds the size safety limit")
	}
	return b, err
}

// OpenFile's creation mode does not tighten an existing export's permissions.
func writePrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Close()
}
