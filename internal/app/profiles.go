package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Profile is a saved connection. Passwords never live here: they are in
// the local encrypted vault under profile/<id>/{redis,ssh,ssh-passphrase,
// sentinel,proxy}.
type Profile struct {
	ConnectionOptions `json:"options,omitempty"`
	Order             int         `json:"order,omitempty"`
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	Group             string      `json:"group,omitempty"`
	Mode              string      `json:"mode,omitempty"` // standalone, sentinel, cluster
	Addr              string      `json:"addr,omitempty"`
	Username          string      `json:"username,omitempty"`
	DB                int         `json:"db"`
	UseTLS            bool        `json:"tls,omitempty"`
	TLS               TLSConfig   `json:"tlsConfig,omitempty"`
	SentinelAddrs     []string    `json:"sentinelAddrs,omitempty"`
	MasterName        string      `json:"masterName,omitempty"`
	SentinelUser      string      `json:"sentinelUsername,omitempty"`
	ClusterAddrs      []string    `json:"clusterAddrs,omitempty"`
	Proxy             ProxyConfig `json:"proxy,omitempty"`
	SSH               SSHConfig   `json:"ssh"`
}

// SSHConfig tunnels the connection through an SSH hop.
type SSHConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	Addr       string `json:"addr,omitempty"` // host:port
	User       string `json:"user,omitempty"`
	KeyPath    string `json:"keyPath,omitempty"`    // private key, with the passphrase in the local vault
	Agent      bool   `json:"agent,omitempty"`      // use SSH_AUTH_SOCK
	Insecure   bool   `json:"insecure,omitempty"`   // skip the known_hosts host key check
	KnownHosts string `json:"knownHosts,omitempty"` // known_hosts file, ~/.ssh/known_hosts by default
}

// TLSConfig is how to trust and present a TLS connection.
type TLSConfig struct {
	CA         string `json:"ca,omitempty"`         // PEM CA bundle
	Cert       string `json:"cert,omitempty"`       // client certificate
	Key        string `json:"key,omitempty"`        // client private key
	ServerName string `json:"serverName,omitempty"` // SNI / verification name
	Insecure   bool   `json:"insecure,omitempty"`   // skip verification
}

// ProxyConfig is an HTTP CONNECT or SOCKS5 hop in front of Redis. The
// password is kept in the local vault, never in the profile.
type ProxyConfig struct {
	Type     string `json:"type,omitempty"` // "", http, socks5
	Addr     string `json:"addr,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"-"`
}

// SecretSet is the saved values a profile needs.
type SecretSet struct {
	Redis, SSH, SSHPassphrase, Sentinel, Proxy string
}

func (p Profile) mode() string {
	switch p.Mode {
	case "sentinel", "cluster":
		return p.Mode
	default:
		return "standalone"
	}
}

func (p Profile) label() string {
	if p.Name != "" {
		return p.Name
	}
	switch p.mode() {
	case "sentinel":
		if p.MasterName != "" {
			return p.MasterName
		}
	case "cluster":
		if len(p.ClusterAddrs) > 0 {
			return p.ClusterAddrs[0]
		}
	}
	return p.Addr
}

// summary is the line under a profile's name in the connection list.
func (p Profile) summary() string {
	switch p.mode() {
	case "sentinel":
		return "sentinel · " + p.MasterName
	case "cluster":
		return "cluster · " + strings.Join(p.ClusterAddrs, ", ")
	default:
		return p.Addr
	}
}

func (p Profile) config(secrets SecretSet) ConnConfig {
	cfg := ConnConfig{
		ConnectionOptions: p.ConnectionOptions,
		Name:              p.Name,
		Mode:              p.mode(),
		Addr:              p.Addr,
		Username:          p.Username,
		Password:          secrets.Redis,
		DB:                p.DB,
		UseTLS:            p.UseTLS,
		TLS:               p.TLS,
		SentinelAddrs:     p.SentinelAddrs,
		MasterName:        p.MasterName,
		SentinelUsername:  p.SentinelUser,
		SentinelPassword:  secrets.Sentinel,
		ClusterAddrs:      p.ClusterAddrs,
		Proxy:             p.Proxy,
		SSH:               p.SSH,
		SSHPassword:       secrets.SSH,
		SSHPassphrase:     secrets.SSHPassphrase,
	}
	cfg.Proxy.Password = secrets.Proxy
	return cfg
}

func newProfileID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// loadProfiles reads the saved profiles from path, returning none when the
// file does not exist yet.
func loadProfiles(path string) ([]Profile, error) {
	data, err := readBoundedFile(path, maxProfileBytes)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []Profile
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, err
	}
	sortProfiles(ps)
	return ps, nil
}

func saveProfiles(path string, ps []Profile) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	// Write beside the file and rename, so a crash cannot leave a half
	// written profiles.json.
	f, err := os.CreateTemp(dir, ".profiles-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sortProfiles(ps []Profile) {
	sort.Slice(ps, func(i, j int) bool {
		gi, gj := strings.ToLower(ps[i].Group), strings.ToLower(ps[j].Group)
		if gi != gj {
			if gi == "" {
				return false
			}
			if gj == "" {
				return true
			}
			return gi < gj
		}
		if ps[i].Order != ps[j].Order {
			return ps[i].Order < ps[j].Order
		}
		return ps[i].label() < ps[j].label()
	})
}
