package secrets

// The app's local password store. The vault and its key are 0600 files in
// the app data directory; AES-GCM protects the values at rest, with the
// current user's file permissions as the boundary.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
)

type localVault struct {
	Version int               `json:"version"`
	Items   map[string]string `json:"items"`
}

type Store struct {
	dir, vaultPath, keyPath string

	mu    sync.Mutex
	key   []byte
	items map[string]string
}

func Open(dir string) (*Store, error) {
	s := &Store{
		dir:       dir,
		vaultPath: filepath.Join(dir, "secrets.vault"),
		keyPath:   filepath.Join(dir, "secrets.key"),
		items:     map[string]string{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

const maxVaultBytes = 4 << 20

func readPrivateFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("local secrets: file exceeds size limit")
	}
	return b, err
}

func (s *Store) load() error {
	data, err := readPrivateFile(s.vaultPath, maxVaultBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var v localVault
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("local secrets: %w", err)
	}
	if v.Items != nil {
		s.items = v.Items
	}
	return nil
}

func (s *Store) ensureKey() error {
	if len(s.key) > 0 {
		return nil
	}
	data, err := readPrivateFile(s.keyPath, 32)
	if errors.Is(err, os.ErrNotExist) {
		data = make([]byte, 32)
		if _, err := rand.Read(data); err != nil {
			return err
		}
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(s.keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if !errors.Is(err, os.ErrExist) {
				return err
			}
			data, err = readPrivateFile(s.keyPath, 32)
			if err != nil {
				return err
			}
		} else {
			if _, err := f.Write(data); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	if len(data) != 32 {
		return errors.New("local secrets: key must contain exactly 32 bytes")
	}
	sum := sha256.Sum256(append([]byte("kivrix-local-secrets-v1\x00"), data...))
	s.key = sum[:]
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(localVault{Version: 1, Items: s.items}, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxVaultBytes {
		return errors.New("local secrets: vault exceeds size limit")
	}
	f, err := os.CreateTemp(s.dir, ".secrets-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.vaultPath)
}

func (s *Store) seal(value []byte) (string, error) {
	if err := s.ensureKey(); err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	data := gcm.Seal(nonce, nonce, value, nil)
	return base64.StdEncoding.EncodeToString(data), nil
}

func (s *Store) open(encoded string) ([]byte, error) {
	if err := s.ensureKey(); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("local secrets: value is truncated")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
}

func (s *Store) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if encoded, ok := s.items[key]; ok {
		return s.open(encoded)
	}
	return nil, mygo.ErrSecretNotFound
}

func (s *Store) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := s.seal(value)
	if err != nil {
		return err
	}
	s.items[key] = encoded
	return s.saveLocked()
}

func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[key]; !ok {
		return nil
	}
	delete(s.items, key)
	return s.saveLocked()
}
