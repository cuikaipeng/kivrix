package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ConnConfig describes one Redis server to connect to.
type ConnConfig struct {
	ConnectionOptions
	Name             string
	Mode             string // standalone, sentinel, cluster; empty means standalone
	Addr             string // host:port for standalone, or a seed
	Username         string
	Password         string
	DB               int
	UseTLS           bool
	TLS              TLSConfig
	SentinelAddrs    []string
	MasterName       string
	SentinelUsername string
	SentinelPassword string
	ClusterAddrs     []string
	Proxy            ProxyConfig
	SSH              SSHConfig
	SSHPassword      string // password auth
	SSHPassphrase    string // private-key passphrase
}

func (c ConnConfig) mode() string {
	switch c.Mode {
	case "sentinel", "cluster":
		return c.Mode
	default:
		return "standalone"
	}
}

func (c ConnConfig) label() string {
	if c.Name != "" {
		return c.Name
	}
	switch c.mode() {
	case "sentinel":
		if c.MasterName != "" {
			return c.MasterName
		}
	case "cluster":
		if len(c.ClusterAddrs) > 0 {
			return c.ClusterAddrs[0]
		}
	}
	return c.Addr
}

// Store is the app's Redis connection. Its methods are safe to call from
// worker goroutines; callers publish results with Window.Update.
type Store struct {
	cfg     ConnConfig
	client  redis.UniversalClient
	cluster *redis.ClusterClient
	ssh     *ssh.Client
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	kind    string
}

// Kind is the connection mode: standalone, sentinel or cluster.
func (s *Store) Kind() string {
	if s.kind == "" {
		return "standalone"
	}
	return s.kind
}

func (s *Store) IsCluster() bool { return s.kind == "cluster" }

// Connect dials the server and verifies it with PING. With SSH enabled the
// Redis connection is tunneled through the SSH hop.
func (s *Store) Connect(cfg ConnConfig) error {
	if cfg.Mode == "" {
		cfg.Mode = "standalone"
	}
	switch cfg.mode() {
	case "sentinel":
		if len(cfg.SentinelAddrs) == 0 {
			return errors.New("sentinel: at least one sentinel address is required")
		}
		if cfg.MasterName == "" {
			return errors.New("sentinel: a master name is required")
		}
	case "cluster":
		if len(cfg.ClusterAddrs) == 0 {
			if cfg.Addr == "" {
				return errors.New("cluster: at least one node address is required")
			}
			cfg.ClusterAddrs = []string{cfg.Addr}
		}
		cfg.DB = 0
	default:
		if cfg.Addr == "" {
			return errors.New("address is required")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.connectionTimeout())
	defer cancel()
	base, err := baseDialer(cfg.Proxy)
	if err != nil {
		return err
	}
	var sshc *ssh.Client
	var dial func(ctx context.Context, network, addr string) (net.Conn, error)
	if cfg.SSH.Enabled {
		sshc, err = dialSSH(ctx, cfg, base)
		if err != nil {
			return err
		}
		dial = tunnelDialer(cfg, func(ctx context.Context, network, addr string) (net.Conn, error) {
			return sshc.DialContext(ctx, network, addr)
		})
	} else {
		dial = tunnelDialer(cfg, base)
	}

	baseDial := dial
	dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, cfg.connectionTimeout())
		defer cancel()
		conn, err := baseDial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return limitReplies(conn), nil
	}
	var cl redis.UniversalClient
	var cluster *redis.ClusterClient
	switch cfg.mode() {
	case "sentinel":
		cl = redis.NewFailoverClient(&redis.FailoverOptions{
			PoolSize: 4, MaxActiveConns: 8, PipelinePoolSize: 2,
			ReadTimeout: time.Duration(cfg.CommandTimeout) * time.Second, WriteTimeout: time.Duration(cfg.CommandTimeout) * time.Second, ContextTimeoutEnabled: true,
			MasterName:       cfg.MasterName,
			SentinelAddrs:    cfg.SentinelAddrs,
			SentinelUsername: cfg.SentinelUsername,
			SentinelPassword: cfg.SentinelPassword,
			Username:         cfg.Username,
			Password:         cfg.Password,
			DB:               cfg.DB,
			Dialer:           dial,
		})
	case "cluster":
		cluster = redis.NewClusterClient(&redis.ClusterOptions{
			NewClient: func(opts *redis.Options) *redis.Client {
				client := redis.NewClient(opts)
				if cfg.ReadOnly {
					client.AddHook(readOnlyHook{})
				}
				return client
			},
			PoolSize: 4, MaxActiveConns: 8, PipelinePoolSize: 2,
			ReadTimeout: time.Duration(cfg.CommandTimeout) * time.Second, WriteTimeout: time.Duration(cfg.CommandTimeout) * time.Second, ContextTimeoutEnabled: true,
			Addrs:    cfg.ClusterAddrs,
			Username: cfg.Username,
			Password: cfg.Password,
			Dialer:   dial,
		})
		cl = cluster
	default:
		cl = redis.NewClient(&redis.Options{
			PoolSize: 4, MaxActiveConns: 8, PipelinePoolSize: 2,
			Network:     cfg.Network,
			ReadTimeout: time.Duration(cfg.CommandTimeout) * time.Second, WriteTimeout: time.Duration(cfg.CommandTimeout) * time.Second, ContextTimeoutEnabled: true,
			Addr:     cfg.Addr,
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
			Dialer:   dial,
		})
	}

	if cfg.ReadOnly {
		cl.AddHook(readOnlyHook{})
	}
	if err := cl.Ping(ctx).Err(); err != nil {
		cl.Close()
		if sshc != nil {
			sshc.Close()
		}
		return err
	}
	s.Close()
	s.cfg, s.client, s.cluster, s.ssh, s.dial, s.kind = cfg, cl, cluster, sshc, dial, cfg.mode()
	return nil
}

// tunnelDialer dials Redis through the SSH hop and, when TLS is enabled,
// wraps the tunneled connection. It applies TLS itself because go-redis
// only wraps TLS in its default dialer, which Dialer replaces.
func tunnelDialer(cfg ConnConfig, dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if !cfg.UseTLS {
			return conn, nil
		}
		tc, err := tlsConfigFor(cfg, addr)
		if err != nil {
			conn.Close()
			return nil, err
		}
		tlsConn := tls.Client(conn, tc)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("tls handshake: %w", err)
		}
		return tlsConn, nil
	}
}

// tlsConfigFor builds a TLS config for one target, with the profile's CA,
// client certificate, SNI override and optional verification opt-out.
func tlsConfigFor(cfg ConnConfig, addr string) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLS.ServerName != "" {
		tc.ServerName = cfg.TLS.ServerName
	} else {
		tc.ServerName = hostOnly(addr)
	}
	if cfg.TLS.Insecure {
		tc.InsecureSkipVerify = true
	}
	if cfg.TLS.CA != "" {
		pem, err := readBoundedFile(expandHome(cfg.TLS.CA), maxProfileBytes)
		if err != nil {
			return nil, fmt.Errorf("tls CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls CA: %s has no PEM certificates", cfg.TLS.CA)
		}
		tc.RootCAs = pool
	}
	if cfg.TLS.Cert != "" || cfg.TLS.Key != "" {
		if cfg.TLS.Cert == "" || cfg.TLS.Key == "" {
			return nil, errors.New("tls: both a client certificate and key are required")
		}
		certPEM, err := readBoundedFile(expandHome(cfg.TLS.Cert), maxProfileBytes)
		if err != nil {
			return nil, fmt.Errorf("tls client certificate: %w", err)
		}
		keyPEM, err := readBoundedFile(expandHome(cfg.TLS.Key), maxProfileBytes)
		if err != nil {
			return nil, fmt.Errorf("tls client key: %w", err)
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("tls client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}

// hostOnly is the host of a host:port address, for TLS verification.
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// expandHome resolves a leading ~ or ~/ in a path typed into the form.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// dialSSH connects to the SSH hop with key or password auth, checking the
// host key against known_hosts unless the profile opts out.
func dialSSH(ctx context.Context, cfg ConnConfig, base func(ctx context.Context, network, addr string) (net.Conn, error)) (*ssh.Client, error) {
	hostKey, err := sshHostKeyCallback(cfg.SSH)
	if err != nil {
		return nil, err
	}
	sc := &ssh.ClientConfig{
		User:            cfg.SSH.User,
		HostKeyCallback: hostKey,
		Timeout:         8 * time.Second,
	}
	var agentConn net.Conn
	if cfg.SSH.KeyPath != "" {
		signer, err := loadSSHSigner(cfg.SSH.KeyPath, cfg.SSHPassphrase)
		if err != nil {
			return nil, err
		}
		sc.Auth = append(sc.Auth, ssh.PublicKeys(signer))
	}
	if cfg.SSHPassword != "" {
		sc.Auth = append(sc.Auth, ssh.Password(cfg.SSHPassword))
	}
	if cfg.SSH.Agent {
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, errors.New("ssh agent: SSH_AUTH_SOCK is not set")
		}
		agentConn, err = (&net.Dialer{}).DialContext(ctx, "unix", sock)
		if err != nil {
			return nil, fmt.Errorf("ssh agent: %w", err)
		}
		deadline := time.Now().Add(8 * time.Second)
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		agentConn.SetDeadline(deadline)
		ag := agent.NewClient(agentConn)
		sc.Auth = append(sc.Auth, ssh.PublicKeysCallback(ag.Signers))
	}
	if len(sc.Auth) == 0 {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, errors.New("ssh: a password, private key or agent is required")
	}
	addr := cfg.SSH.Addr
	if !strings.Contains(addr, ":") {
		addr += ":22"
	}
	conn, err := base(ctx, "tcp", addr)
	if err != nil {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	conn.SetDeadline(deadline)
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, sc)
	conn.SetDeadline(time.Time{})
	if agentConn != nil {
		agentConn.Close()
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

// sshHostKeyCallback verifies a host key against known_hosts, or accepts
// any key when SSH.Insecure asks to skip the check.
// loadSSHSigner reads a private key, decrypting it with passphrase when
// one is supplied.
func loadSSHSigner(path, passphrase string) (ssh.Signer, error) {
	key, err := readBoundedFile(expandHome(path), maxProfileBytes)
	if err != nil {
		return nil, err
	}
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
	}
	return ssh.ParsePrivateKey(key)
}

func sshHostKeyCallback(cfg SSHConfig) (ssh.HostKeyCallback, error) {
	if cfg.Insecure {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	path := cfg.KnownHosts
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("ssh: find home directory for known_hosts: %w", err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("ssh: read known_hosts %s: %w (or enable \"Skip host key check\")", path, err)
	}
	return cb, nil
}

func (s *Store) Close() {
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	if s.ssh != nil {
		s.ssh.Close()
		s.ssh = nil
	}
	s.cluster = nil
	s.dial = nil
	s.kind = ""
}

func (s *Store) Connected() bool { return s.client != nil }

var errNotConnected = errors.New("not connected")

// ScanKeys scans keys matching pattern (glob, empty means all) and reports
// them in batches. It stops when limit keys were found (0 means no limit)
// or ctx is canceled, and reports done=false when the limit cut the scan
// short.
func (s *Store) ScanKeys(ctx context.Context, pattern string, limit int, onBatch func(keys []string)) (done bool, n int, err error) {
	if s.client == nil {
		return false, 0, errNotConnected
	}
	if pattern == "" {
		pattern = "*"
	}
	cursor := ""
	for {
		var keys []string
		var next string
		keys, next, err = s.ScanPage(ctx, cursor, pattern, 500)
		if err != nil {
			return false, n, err
		}
		if limit > 0 && n+len(keys) > limit {
			keys = keys[:limit-n]
			sort.Strings(keys)
			onBatch(keys)
			n += len(keys)
			return false, n, nil
		}
		if len(keys) > 0 {
			sort.Strings(keys)
			onBatch(keys)
			n += len(keys)
		}
		if next == "" {
			return true, n, nil
		}
		cursor = next
	}
}

// Value is one key's content, flattened for display.
type Value struct {
	Type   string // string, list, hash, set, zset, stream, json, none
	TTL    time.Duration
	Size   int64       // elements in the key (STRLEN, LLEN, ...)
	Str    string      // Type == string or json
	Pairs  [][2]string // hash: field/value; zset: member/score
	Items  []string    // list, set
	Stream []StreamEntry
	Memory int64  // MEMORY USAGE, 0 when unavailable
	LastID string // stream's last generated ID
	Groups int64  // stream consumer group count
	// Truncated reports the value has more rows than shown.
	Truncated   bool
	PreviewOnly bool // a bounded prefix of an oversized string; never writable
}

func (s *Store) stringPreview(ctx context.Context, key string) (ValuePage, error) {
	v := Value{Type: "string", TTL: -1, PreviewOnly: true}
	var err error
	v.Size, err = s.client.StrLen(ctx, key).Result()
	if err != nil {
		return ValuePage{}, err
	}
	v.Str, err = s.client.GetRange(ctx, key, 0, (64<<10)-1).Result()
	if err != nil {
		return ValuePage{}, err
	}
	v.TTL, _ = s.client.TTL(ctx, key).Result()
	return ValuePage{Value: v}, nil
}

// StreamEntry is one entry in a Redis stream.
type StreamEntry struct {
	ID     string
	Fields [][2]string
}

const (
	valueRowCap = 1000
)

// ValuePage is one page of a value, with where the next page starts.
type ValuePage struct {
	Value
	NextCursor  uint64
	NextOffset  int
	HasMore     bool
	ListIndices []int // server indices of filtered list rows
}

// GetValue reads one key's first page, capped at valueRowCap rows.
func (s *Store) GetValue(ctx context.Context, key string) (Value, error) {
	p, err := s.GetValuePage(ctx, key, 0, 0, valueRowCap, "")
	v := p.Value
	if len(v.Pairs) > valueRowCap {
		v.Pairs = v.Pairs[:valueRowCap]
	}
	if len(v.Items) > valueRowCap {
		v.Items = v.Items[:valueRowCap]
	}
	v.Truncated = p.HasMore || p.Truncated || len(v.Pairs) < len(p.Value.Pairs) || len(v.Items) < len(p.Value.Items)
	return v, err
}

// GetValuePage reads one page of a key. match filters hash fields, set
// members and sorted-set members; lists are read by offset. count is the
// page size asked of Redis, which may return a few more rows.
func (s *Store) GetValuePage(ctx context.Context, key string, cursor uint64, offset, count int, match string) (ValuePage, error) {
	if s.client == nil {
		return ValuePage{}, errNotConnected
	}
	if count <= 0 {
		count = valueRowCap
	}
	v := Value{TTL: -1}
	typ, err := s.client.Type(ctx, key).Result()
	if err != nil {
		return ValuePage{Value: v}, err
	}
	v.Type = typ
	if typ == "none" {
		return ValuePage{Value: v}, nil
	}
	if ttl, err := s.client.TTL(ctx, key).Result(); err == nil {
		v.TTL = ttl
	}
	if mem, err := s.client.MemoryUsage(ctx, key).Result(); err == nil {
		v.Memory = mem
	}
	page := ValuePage{Value: v}
	switch typ {
	case "string":
		v.Str, err = s.client.GetRange(ctx, key, 0, maxEditorBytes).Result()
		if len(v.Str) > maxEditorBytes {
			return page, errValueLimit
		}
		if err == nil {
			v.Size = int64(len(v.Str))
		}
		page.Value = v
		return page, err
	case "list":
		v.Size, err = s.client.LLen(ctx, key).Result()
		if err != nil {
			page.Value = v
			return page, err
		}
		if match != "" {
			q := strings.TrimSuffix(strings.TrimPrefix(match, "*"), "*")
			next := max(offset, 0)
			for int64(next) < v.Size && len(v.Items) < count {
				items, scanErr := s.client.LRange(ctx, key, int64(next), int64(next+max(count, 200)-1)).Result()
				if scanErr != nil {
					return page, scanErr
				}
				if len(items) == 0 {
					next = int(v.Size)
					break
				}
				for _, item := range items {
					index := next
					next++
					if strings.Contains(item, q) {
						v.Items = append(v.Items, item)
						page.ListIndices = append(page.ListIndices, index)
						if len(v.Items) == count {
							break
						}
					}
				}
			}
			page.NextOffset, page.HasMore = next, int64(next) < v.Size
			break
		}
		start := offset
		if start < 0 {
			start = 0
		}
		end := start + count - 1
		v.Items, err = s.client.LRange(ctx, key, int64(start), int64(end)).Result()
		if err != nil {
			page.Value = v
			return page, err
		}
		page.NextOffset = start + len(v.Items)
		page.HasMore = int64(page.NextOffset) < v.Size
	case "hash":
		v.Size, err = s.client.HLen(ctx, key).Result()
		if err != nil {
			break
		}
		next := cursor
		for len(v.Pairs) < count {
			var fields []string
			fields, next, err = s.client.HScan(ctx, key, next, match, int64(count)).Result()
			if err != nil {
				break
			}
			for i := 0; i+1 < len(fields); i += 2 {
				v.Pairs = append(v.Pairs, [2]string{fields[i], fields[i+1]})
			}
			if next == 0 {
				break
			}
		}
		if err != nil {
			break
		}
		sort.Slice(v.Pairs, func(i, j int) bool { return v.Pairs[i][0] < v.Pairs[j][0] })
		page.NextCursor = next
		page.HasMore = next != 0
	case "set":
		v.Size, err = s.client.SCard(ctx, key).Result()
		if err != nil {
			break
		}
		next := cursor
		for len(v.Items) < count {
			var vals []string
			vals, next, err = s.client.SScan(ctx, key, next, match, int64(count)).Result()
			if err != nil {
				break
			}
			v.Items = append(v.Items, vals...)
			if next == 0 {
				break
			}
		}
		if err != nil {
			break
		}
		sort.Strings(v.Items)
		page.NextCursor = next
		page.HasMore = next != 0
	case "zset":
		v.Size, err = s.client.ZCard(ctx, key).Result()
		if err != nil {
			break
		}
		if match == "" {
			// A sorted set keeps its score order: read it by offset.
			start := offset
			if start < 0 {
				start = 0
			}
			var zs []redis.Z
			zs, err = s.client.ZRangeWithScores(ctx, key, int64(start), int64(start+count-1)).Result()
			if err != nil {
				break
			}
			for _, z := range zs {
				v.Pairs = append(v.Pairs, [2]string{fmt.Sprint(z.Member), formatFloat(z.Score)})
			}
			page.NextOffset = start + len(zs)
			page.HasMore = int64(page.NextOffset) < v.Size
			break
		}
		next := cursor
		var zs []redis.Z
		for len(zs) < count {
			var vals []string
			vals, next, err = s.client.ZScan(ctx, key, next, match, int64(count)).Result()
			if err != nil {
				break
			}
			for i := 0; i+1 < len(vals); i += 2 {
				score, _ := strconv.ParseFloat(vals[i+1], 64)
				zs = append(zs, redis.Z{Score: score, Member: vals[i]})
			}
			if next == 0 {
				break
			}
		}
		if err != nil {
			break
		}
		sort.Slice(zs, func(i, j int) bool {
			if zs[i].Score == zs[j].Score {
				return fmt.Sprint(zs[i].Member) < fmt.Sprint(zs[j].Member)
			}
			return zs[i].Score < zs[j].Score
		})
		for _, z := range zs {
			v.Pairs = append(v.Pairs, [2]string{fmt.Sprint(z.Member), formatFloat(z.Score)})
		}
		page.NextCursor = next
		page.HasMore = next != 0
	case "stream":
		v.Size, err = s.client.XLen(ctx, key).Result()
		if err != nil {
			break
		}
		info, infoErr := s.client.XInfoStream(ctx, key).Result()
		if infoErr == nil {
			v.LastID = info.LastGeneratedID
			v.Groups = info.Groups
		}
		msgs, err := s.client.XRevRangeN(ctx, key, "+", "-", int64(count)).Result()
		if err != nil {
			break
		}
		for _, m := range msgs {
			fields := make([]string, 0, len(m.Values))
			for field := range m.Values {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			entry := StreamEntry{ID: m.ID}
			for _, field := range fields {
				entry.Fields = append(entry.Fields, [2]string{field, fmt.Sprint(m.Values[field])})
			}
			v.Stream = append(v.Stream, entry)
		}
		v.Truncated = int64(len(v.Stream)) < v.Size
		page.HasMore = false
	case "ReJSON-RL", "rejson-rl", "json":
		v.Type = "json"
		v.Str, err = s.JSONGet(ctx, key)
		if len(v.Str) > maxEditorBytes {
			return page, errValueLimit
		}
		if err == nil {
			v.Size = int64(len(v.Str))
		}
	default:
		err = fmt.Errorf("type %q is not supported yet", typ)
	}
	page.Value = v
	return page, err
}

// JSONGet reads a RedisJSON value at the root.
func (s *Store) JSONGet(ctx context.Context, key string) (string, error) {
	if s.client == nil {
		return "", errNotConnected
	}
	return s.client.Do(ctx, "JSON.GET", key).Text()
}

// JSONSet replaces a RedisJSON value at the root.
func (s *Store) JSONSet(ctx context.Context, key, value string) error {
	if s.client == nil {
		return errNotConnected
	}
	if !json.Valid([]byte(value)) {
		return errors.New("value is not valid JSON")
	}
	return s.client.Do(ctx, "JSON.SET", key, ".", value).Err()
}

// StreamAdd appends a stream entry and returns its ID.
func (s *Store) StreamAdd(ctx context.Context, key, id string, fields [][2]string) (string, error) {
	if s.client == nil {
		return "", errNotConnected
	}
	if len(fields) == 0 {
		return "", errors.New("a stream entry needs at least one field")
	}
	if id == "" {
		id = "*"
	}
	values := make([]string, 0, len(fields)*2)
	for _, f := range fields {
		values = append(values, f[0], f[1])
	}
	return s.client.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: id, Values: values}).Result()
}

// StreamBefore reads up to count entries older than before, newest first.
// before is exclusive; pass "" for the newest entries.
func (s *Store) StreamBefore(ctx context.Context, key, before string, count int) ([]StreamEntry, error) {
	if s.client == nil {
		return nil, errNotConnected
	}
	if count <= 0 {
		count = valuePageSize
	}
	if before == "" {
		before = "+"
	}
	msgs, err := s.client.XRevRangeN(ctx, key, before, "-", int64(count+1)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]StreamEntry, 0, len(msgs))
	for _, m := range msgs {
		if m.ID == before {
			continue
		}
		fields := make([]string, 0, len(m.Values))
		for field := range m.Values {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		entry := StreamEntry{ID: m.ID}
		for _, field := range fields {
			entry.Fields = append(entry.Fields, [2]string{field, fmt.Sprint(m.Values[field])})
		}
		out = append(out, entry)
		if len(out) == count {
			break
		}
	}
	return out, nil
}

// StreamGroup is one XINFO GROUPS row.
type StreamGroup struct {
	Name            string
	Consumers       int64
	Pending         int64
	LastDeliveredID string
	Lag             int64
}

// StreamGroups lists a stream's consumer groups.
func (s *Store) StreamGroups(ctx context.Context, key string) ([]StreamGroup, error) {
	if s.client == nil {
		return nil, errNotConnected
	}
	groups, err := s.client.XInfoGroups(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	out := make([]StreamGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, StreamGroup{
			Name: g.Name, Consumers: g.Consumers, Pending: g.Pending,
			LastDeliveredID: g.LastDeliveredID, Lag: g.Lag,
		})
	}
	return out, nil
}

// StreamGroupCreate creates a group at start ("$" for new messages).
func (s *Store) StreamGroupCreate(ctx context.Context, key, group, start string) error {
	if s.client == nil {
		return errNotConnected
	}
	if strings.TrimSpace(start) == "" {
		start = "$"
	}
	return s.client.XGroupCreateMkStream(ctx, key, group, start).Err()
}

// StreamGroupDestroy removes a consumer group.
func (s *Store) StreamGroupDestroy(ctx context.Context, key, group string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.XGroupDestroy(ctx, key, group).Err()
}

// StreamConsumer is one XINFO CONSUMERS row.
type StreamConsumer struct {
	Name    string
	Pending int64
	Idle    time.Duration
}

// StreamPending is one XPENDING entry.
type StreamPending struct {
	ID         string
	Consumer   string
	Idle       time.Duration
	RetryCount int64
}

// StreamConsumers lists a group's consumers.
func (s *Store) StreamConsumers(ctx context.Context, key, group string) ([]StreamConsumer, error) {
	if s.client == nil {
		return nil, errNotConnected
	}
	cs, err := s.client.XInfoConsumers(ctx, key, group).Result()
	if err != nil {
		return nil, err
	}
	out := make([]StreamConsumer, 0, len(cs))
	for _, c := range cs {
		out = append(out, StreamConsumer{Name: c.Name, Pending: c.Pending, Idle: c.Idle})
	}
	return out, nil
}

// StreamPending lists a group's pending entries.
func (s *Store) StreamPending(ctx context.Context, key, group string, count int64) ([]StreamPending, error) {
	return s.StreamPendingPage(ctx, key, group, "-", count)
}

// StreamPendingPage reads pending entries from start (usually the last ID
// already shown, or "-").
func (s *Store) StreamPendingPage(ctx context.Context, key, group, start string, count int64) ([]StreamPending, error) {
	if s.client == nil {
		return nil, errNotConnected
	}
	if count <= 0 {
		count = 200
	}
	if start == "" {
		start = "-"
	}
	ps, err := s.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: key, Group: group, Start: start, End: "+", Count: count + 1,
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]StreamPending, 0, len(ps))
	for _, p := range ps {
		if start != "-" && p.ID == start {
			continue
		}
		out = append(out, StreamPending{ID: p.ID, Consumer: p.Consumer, Idle: p.Idle, RetryCount: p.RetryCount})
		if int64(len(out)) == count {
			break
		}
	}
	return out, nil
}

// StreamAck acknowledges pending entries.
func (s *Store) StreamAck(ctx context.Context, key, group string, ids ...string) (int64, error) {
	if s.client == nil {
		return 0, errNotConnected
	}
	return s.client.XAck(ctx, key, group, ids...).Result()
}

// StreamConsumerDelete removes a consumer and its pending entries from a
// group.
func (s *Store) StreamConsumerDelete(ctx context.Context, key, group, consumer string) (int64, error) {
	if s.client == nil {
		return 0, errNotConnected
	}
	return s.client.XGroupDelConsumer(ctx, key, group, consumer).Result()
}

// StreamAutoClaim claims up to count pending entries idle at least minIdle
// for consumer. It returns the claimed count and the next cursor.
func (s *Store) StreamAutoClaim(ctx context.Context, key, group, consumer string, minIdle time.Duration, start string, count int64) (int, string, error) {
	if s.client == nil {
		return 0, "", errNotConnected
	}
	if strings.TrimSpace(consumer) == "" {
		return 0, "", errors.New("consumer name is empty")
	}
	if start == "" {
		start = "0-0"
	}
	if count <= 0 {
		count = 200
	}
	messages, next, err := s.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: key, Group: group, Consumer: consumer, MinIdle: minIdle, Start: start, Count: count,
	}).Result()
	if err != nil {
		return 0, "", err
	}
	return len(messages), next, nil
}

// StreamTrim trims a stream by MAXLEN or MINID.
func (s *Store) StreamTrim(ctx context.Context, key, mode string, value string, approx bool) (int64, error) {
	if s.client == nil {
		return 0, errNotConnected
	}
	switch strings.ToUpper(mode) {
	case "MINID":
		if strings.TrimSpace(value) == "" {
			return 0, errors.New("min ID is empty")
		}
		if approx {
			return s.client.XTrimMinIDApprox(ctx, key, strings.TrimSpace(value), 0).Result()
		}
		return s.client.XTrimMinID(ctx, key, strings.TrimSpace(value)).Result()
	default:
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || n < 0 {
			return 0, errors.New("max length is not a number")
		}
		if approx {
			return s.client.XTrimMaxLenApprox(ctx, key, n, 0).Result()
		}
		return s.client.XTrimMaxLen(ctx, key, n).Result()
	}
}

// StreamClaim claims a pending entry for consumer.
func (s *Store) StreamClaim(ctx context.Context, key, group, consumer, id string) error {
	if s.client == nil {
		return errNotConnected
	}
	if strings.TrimSpace(consumer) == "" {
		return errors.New("consumer name is empty")
	}
	return s.client.XClaim(ctx, &redis.XClaimArgs{
		Stream: key, Group: group, Consumer: consumer, MinIdle: 0, Messages: []string{id},
	}).Err()
}

// ClientList returns the raw CLIENT LIST text from one node.
func (s *Store) ClientList(ctx context.Context) (string, error) {
	if s.client == nil {
		return "", errNotConnected
	}
	return s.client.ClientList(ctx).Result()
}

// StreamDel removes one stream entry.
func (s *Store) StreamDel(ctx context.Context, key, id string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.XDel(ctx, key, id).Err()
}

// SetString replaces a string key's value, keeping its TTL.
func (s *Store) SetString(ctx context.Context, key, value string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.Set(ctx, key, value, -1).Err()
}

// DeleteKeys removes keys and reports how many existed.
func (s *Store) DeleteKeys(ctx context.Context, keys ...string) (int64, error) {
	if s.client == nil {
		return 0, errNotConnected
	}
	if s.IsCluster() {
		// A cluster rejects DEL of keys in different slots.
		var total int64
		for _, key := range keys {
			n, err := s.client.Unlink(ctx, key).Result()
			if err != nil {
				return total, err
			}
			total += n
		}
		return total, nil
	}
	return s.client.Del(ctx, keys...).Result()
}

// DeletePrefix removes every key whose name starts with prefix, discovered
// with SCAN so it does not depend on what the key browser loaded. It scans
// again until a pass finds nothing, because deleting while SCAN walks the
// keyspace can hide keys from the cursor.
func (s *Store) DeletePrefix(ctx context.Context, prefix string, progress ...func(int64)) (int64, error) {
	if s.client == nil {
		return 0, errNotConnected
	}
	if prefix == "" {
		return 0, errors.New("delete prefix: empty prefix")
	}
	pattern := escapeGlob(prefix) + "*"
	var total int64
	for round := 0; round < 10; round++ {
		found := 0
		var unlinkErr error
		_, _, scanErr := s.ScanKeys(ctx, pattern, 0, func(keys []string) {
			if unlinkErr != nil || len(keys) == 0 {
				return
			}
			if s.IsCluster() {
				for _, key := range keys {
					n, err := s.client.Unlink(ctx, key).Result()
					if err != nil {
						unlinkErr = err
						return
					}
					total += n
				}
			} else {
				n, err := s.client.Unlink(ctx, keys...).Result()
				if err != nil {
					unlinkErr = err
					return
				}
				total += n
			}
			found += len(keys)
			for _, fn := range progress {
				fn(total)
			}
		})
		if scanErr != nil {
			return total, scanErr
		}
		if unlinkErr != nil {
			return total, unlinkErr
		}
		if found == 0 {
			return total, nil
		}
	}
	return total, fmt.Errorf("delete prefix %q: keys kept reappearing", prefix)
}

// escapeGlob escapes the characters Redis' glob matching gives a meaning,
// so a prefix matches literally.
func escapeGlob(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ScanPage returns one SCAN page and its next cursor, so the browser can
// load a large keyspace in bounded steps.
func (s *Store) ScanPage(ctx context.Context, cursor, pattern string, count int64, keyTypes ...string) ([]string, string, error) {
	typ := ""
	if len(keyTypes) > 0 {
		typ = keyTypes[0]
	}
	if s.client == nil {
		return nil, "", errNotConnected
	}
	if pattern == "" {
		pattern = "*"
	}
	if count <= 0 {
		count = 500
	}
	if s.IsCluster() {
		return s.scanClusterPage(ctx, cursor, pattern, count, typ)
	}
	var cur uint64
	if cursor != "" {
		var err error
		cur, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("scan cursor %q: %w", cursor, err)
		}
	}
	keys, next, err := scanCommand(ctx, s.client, cur, pattern, count, typ)
	if err != nil {
		return nil, "", err
	}
	sort.Strings(keys)
	if next == 0 {
		return keys, "", nil
	}
	return keys, strconv.FormatUint(next, 10), nil
}

type scanNode struct {
	addr   string
	client *redis.Client
}

// scanClusterPage walks one master at a time, encoding the node address in
// an opaque cursor so the browser can page across a cluster.
func (s *Store) scanClusterPage(ctx context.Context, cursor, pattern string, count int64, typ string) ([]string, string, error) {
	if s.cluster == nil {
		return nil, "", errors.New("not a cluster connection")
	}
	var nodes []scanNode
	var mu sync.Mutex
	err := s.cluster.ForEachMaster(ctx, func(ctx context.Context, c *redis.Client) error {
		mu.Lock()
		nodes = append(nodes, scanNode{addr: c.Options().Addr, client: c})
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if len(nodes) == 0 {
		return nil, "", errors.New("cluster has no master nodes")
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].addr < nodes[j].addr })
	addr, curText := "", "0"
	if cursor != "" {
		if a, rest, ok := strings.Cut(cursor, "|"); ok {
			addr, curText = a, rest
		} else {
			curText = cursor
		}
	}
	idx := 0
	if addr != "" {
		found := false
		for i := range nodes {
			if nodes[i].addr == addr {
				idx, found = i, true
				break
			}
		}
		if !found {
			curText = "0"
		}
	}
	for idx < len(nodes) {
		cur, err := strconv.ParseUint(curText, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("scan cursor %q: %w", curText, err)
		}
		keys, next, err := scanCommand(ctx, nodes[idx].client, cur, pattern, count, typ)
		if err != nil {
			return nil, "", err
		}
		sort.Strings(keys)
		if next != 0 {
			return keys, nodes[idx].addr + "|" + strconv.FormatUint(next, 10), nil
		}
		if len(keys) > 0 {
			if idx+1 < len(nodes) {
				return keys, nodes[idx+1].addr + "|0", nil
			}
			return keys, "", nil
		}
		if next != 0 {
			curText = strconv.FormatUint(next, 10)
			continue
		}
		// This master is exhausted; continue at the next one.
		idx++
		if idx < len(nodes) {
			curText = "0"
		}
	}
	return nil, "", nil
}

// SelectDB reconnects the client in db, keeping the connection settings.
func (s *Store) SelectDB(ctx context.Context, db int) error {
	if s.client == nil {
		return errNotConnected
	}
	if s.IsCluster() {
		return errors.New("a Redis cluster only has db0")
	}
	if db < 0 || db == s.cfg.DB {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := s.cfg
	cfg.DB = db
	return s.Connect(cfg)
}

// NumDatabases returns the server's DATABASES setting, 16 when it cannot
// be read.
func (s *Store) NumDatabases(ctx context.Context) (int, error) {
	if s.client == nil {
		return 0, errNotConnected
	}
	if s.IsCluster() {
		return 1, nil
	}
	m, err := s.client.ConfigGet(ctx, "databases").Result()
	if err != nil {
		return 16, err
	}
	n, err := strconv.Atoi(m["databases"])
	if err != nil || n <= 0 {
		return 16, nil
	}
	return n, nil
}

// Keyspace returns the key count of each non-empty database, from INFO
// keyspace.
func (s *Store) Keyspace(ctx context.Context) (map[int]int64, error) {
	if s.client == nil {
		return nil, errNotConnected
	}
	if s.cluster != nil {
		var total int64
		var mu sync.Mutex
		err := s.cluster.ForEachMaster(ctx, func(ctx context.Context, c *redis.Client) error {
			n, err := c.DBSize(ctx).Result()
			mu.Lock()
			total += n
			mu.Unlock()
			return err
		})
		if err != nil {
			return nil, err
		}
		return map[int]int64{0: total}, nil
	}
	info, err := s.client.Info(ctx, "keyspace").Result()
	if err != nil {
		return nil, err
	}
	out := map[int]int64{}
	for _, line := range strings.Split(info, "\r\n") {
		if !strings.HasPrefix(line, "db") {
			continue
		}
		rest := strings.TrimPrefix(line, "db")
		colon := strings.IndexByte(rest, ':')
		if colon < 0 {
			continue
		}
		db, err := strconv.Atoi(rest[:colon])
		if err != nil {
			continue
		}
		for _, part := range strings.Split(rest[colon+1:], ",") {
			if v, ok := strings.CutPrefix(part, "keys="); ok {
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					out[db] = n
				}
			}
		}
	}
	return out, nil
}

// NewKeyValue is what a new key holds, by type.
type NewKeyValue struct {
	String string
	Fields map[string]string
	Items  []string
	Scores map[string]float64
	Stream []StreamEntry
}

// CreateKey makes a key of one of the basic types, with an optional TTL.
func (s *Store) CreateKey(ctx context.Context, key, typ string, v NewKeyValue, ttl time.Duration) error {
	if s.client == nil {
		return errNotConnected
	}
	if key == "" {
		return errors.New("key name is empty")
	}
	exists, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("key %q already exists", key)
	}
	pipe := s.client.TxPipeline()
	switch typ {
	case "string":
		pipe.Set(ctx, key, v.String, 0)
	case "hash":
		if len(v.Fields) == 0 {
			return errors.New("a hash needs at least one field")
		}
		args := make([]any, 0, len(v.Fields)*2)
		for f, val := range v.Fields {
			args = append(args, f, val)
		}
		pipe.HSet(ctx, key, args...)
	case "list":
		if len(v.Items) == 0 {
			return errors.New("a list needs at least one element")
		}
		args := make([]any, len(v.Items))
		for i, item := range v.Items {
			args[i] = item
		}
		pipe.RPush(ctx, key, args...)
	case "set":
		if len(v.Items) == 0 {
			return errors.New("a set needs at least one member")
		}
		args := make([]any, len(v.Items))
		for i, item := range v.Items {
			args[i] = item
		}
		pipe.SAdd(ctx, key, args...)
	case "zset":
		if len(v.Scores) == 0 {
			return errors.New("a sorted set needs at least one member")
		}
		zs := make([]redis.Z, 0, len(v.Scores))
		for member, score := range v.Scores {
			zs = append(zs, redis.Z{Score: score, Member: member})
		}
		pipe.ZAdd(ctx, key, zs...)
	case "json":
		if !json.Valid([]byte(v.String)) {
			return errors.New("value is not valid JSON")
		}
		pipe.Do(ctx, "JSON.SET", key, ".", v.String)
	case "stream":
		if len(v.Stream) == 0 {
			return errors.New("a stream needs at least one entry")
		}
		for _, entry := range v.Stream {
			id := entry.ID
			if id == "" {
				id = "*"
			}
			vals := make([]string, 0, len(entry.Fields)*2)
			for _, f := range entry.Fields {
				vals = append(vals, f[0], f[1])
			}
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: id, Values: vals})
		}
	default:
		return fmt.Errorf("type %q is not supported yet", typ)
	}
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err = pipe.Exec(ctx)
	return err
}

// RenameKey renames old to new, refusing to overwrite an existing key.
func (s *Store) RenameKey(ctx context.Context, old, new string) error {
	if s.client == nil {
		return errNotConnected
	}
	if new == "" {
		return errors.New("new key name is empty")
	}
	if old == new {
		return nil
	}
	if s.IsCluster() {
		// RENAME cannot cross a cluster hash slot. Copy the serialized value
		// to the new slot and remove the old key.
		exists, err := s.client.Exists(ctx, new).Result()
		if err != nil {
			return err
		}
		if exists > 0 {
			return fmt.Errorf("key %q already exists", new)
		}
		dump, err := s.client.Dump(ctx, old).Result()
		if err != nil {
			return err
		}
		pttl, err := s.client.PTTL(ctx, old).Result()
		if err != nil {
			return err
		}
		if pttl == -2 {
			return fmt.Errorf("key %q does not exist", old)
		}
		ttl := pttl
		if ttl < 0 {
			ttl = 0
		}
		if err := s.client.RestoreReplace(ctx, new, ttl, dump).Err(); err != nil {
			return err
		}
		return s.client.Del(ctx, old).Err()
	}
	ok, err := s.client.RenameNX(ctx, old, new).Result()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("key %q already exists", new)
	}
	return nil
}

// SetTTL expires key after ttl, or persists it when ttl is zero.
func (s *Store) SetTTL(ctx context.Context, key string, ttl time.Duration) error {
	if s.client == nil {
		return errNotConnected
	}
	if ttl <= 0 {
		ok, err := s.client.Persist(ctx, key).Result()
		if err != nil {
			return err
		}
		if !ok {
			exists, err := s.client.Exists(ctx, key).Result()
			if err != nil {
				return err
			}
			if exists == 0 {
				return fmt.Errorf("key %q does not exist", key)
			}
		}
		return nil
	}
	ok, err := s.client.Expire(ctx, key, ttl).Result()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("key %q does not exist", key)
	}
	return nil
}

// FlushDB empties the selected database.
func (s *Store) FlushDB(ctx context.Context) error {
	if s.client == nil {
		return errNotConnected
	}
	if s.cluster != nil {
		return s.cluster.ForEachMaster(ctx, func(ctx context.Context, c *redis.Client) error {
			return c.FlushDB(ctx).Err()
		})
	}
	return s.client.FlushDB(ctx).Err()
}

// ServerInfo returns a short "version, mode" description of the server.
func (s *Store) ServerInfo(ctx context.Context) (string, error) {
	if s.client == nil {
		return "", errNotConnected
	}
	res, err := s.client.Info(ctx, "server").Result()
	if err != nil {
		return "", err
	}
	return parseServerInfo(res), nil
}

func parseServerInfo(info string) string {
	var version, mode string
	for _, line := range strings.Split(info, "\r\n") {
		if v, ok := strings.CutPrefix(line, "redis_version:"); ok {
			version = v
		}
		if v, ok := strings.CutPrefix(line, "redis_mode:"); ok {
			mode = v
		}
	}
	return fmt.Sprintf("Redis %s · %s", version, mode)
}

func formatFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
}

// HashSet writes a hash field, renaming oldField to field when it changed.
func (s *Store) HashSet(ctx context.Context, key, oldField, field, value string) error {
	if s.client == nil {
		return errNotConnected
	}
	if field == "" {
		return errors.New("hash field is empty")
	}
	if oldField == "" {
		exists, err := s.client.HExists(ctx, key, field).Result()
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("field %q already exists", field)
		}
	}
	if oldField != "" && oldField != field {
		exists, err := s.client.HExists(ctx, key, field).Result()
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("field %q already exists", field)
		}
		pipe := s.client.TxPipeline()
		pipe.HDel(ctx, key, oldField)
		pipe.HSet(ctx, key, field, value)
		_, err = pipe.Exec(ctx)
		return err
	}
	return s.client.HSet(ctx, key, field, value).Err()
}

// HashDel removes a hash field.
func (s *Store) HashDel(ctx context.Context, key, field string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.HDel(ctx, key, field).Err()
}

// ListSet replaces the element at index.
func (s *Store) ListSet(ctx context.Context, key string, index int, value string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.LSet(ctx, key, int64(index), value).Err()
}

// ListPush appends an element.
func (s *Store) ListPush(ctx context.Context, key, value string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.RPush(ctx, key, value).Err()
}

// listDelScript swaps the element at index for a unique marker, then
// removes the marker: LREM alone would remove the first equal value, not
// the row the user chose.
var listDelScript = redis.NewScript(`
local v = redis.call('LINDEX', KEYS[1], ARGV[1])
if not v then return 0 end
redis.call('LSET', KEYS[1], ARGV[1], ARGV[2])
return redis.call('LREM', KEYS[1], 1, ARGV[2])
`)

// ListDel removes the element at index.
func (s *Store) ListDel(ctx context.Context, key string, index int) error {
	if s.client == nil {
		return errNotConnected
	}
	marker := fmt.Sprintf("__redis_pilot_del_%d_%d__", time.Now().UnixNano(), index)
	n, err := listDelScript.Run(ctx, s.client, []string{key}, index, marker).Int()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no element at index %d", index)
	}
	return nil
}

// SetAdd adds a member.
func (s *Store) SetAdd(ctx context.Context, key, member string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.SAdd(ctx, key, member).Err()
}

// SetDel removes a member.
func (s *Store) SetDel(ctx context.Context, key, member string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.SRem(ctx, key, member).Err()
}

// SetReplace renames a member, which Redis models as removing and adding.
func (s *Store) SetReplace(ctx context.Context, key, oldMember, member string) error {
	if s.client == nil {
		return errNotConnected
	}
	if member == "" {
		return errors.New("set member is empty")
	}
	if oldMember == member {
		return nil
	}
	pipe := s.client.TxPipeline()
	pipe.SRem(ctx, key, oldMember)
	pipe.SAdd(ctx, key, member)
	_, err := pipe.Exec(ctx)
	return err
}

// ZSetAdd adds a sorted-set member with its score.
func (s *Store) ZSetAdd(ctx context.Context, key, member string, score float64) error {
	if s.client == nil {
		return errNotConnected
	}
	if _, err := s.client.ZScore(ctx, key, member).Result(); err == nil {
		return fmt.Errorf("member %q already exists", member)
	} else if !errors.Is(err, redis.Nil) {
		return err
	}
	return s.client.ZAdd(ctx, key, redis.Z{Score: score, Member: member}).Err()
}

// ZSetDel removes a sorted-set member.
func (s *Store) ZSetDel(ctx context.Context, key, member string) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.ZRem(ctx, key, member).Err()
}

// ZSetReplace renames a member and sets its score.
func (s *Store) ZSetReplace(ctx context.Context, key, oldMember, member string, score float64) error {
	if s.client == nil {
		return errNotConnected
	}
	if member == "" {
		return errors.New("sorted-set member is empty")
	}
	if oldMember != member {
		if _, err := s.client.ZScore(ctx, key, member).Result(); err == nil {
			return fmt.Errorf("member %q already exists", member)
		} else if !errors.Is(err, redis.Nil) {
			return err
		}
	}
	pipe := s.client.TxPipeline()
	if oldMember != member {
		pipe.ZRem(ctx, key, oldMember)
	}
	pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: member})
	_, err := pipe.Exec(ctx)
	return err
}

// AddHook installs a go-redis hook, used for the command log.
func (s *Store) AddHook(h redis.Hook) {
	if s.client != nil {
		s.client.AddHook(h)
	}
}

// Info returns the server's INFO text.
func (s *Store) Info(ctx context.Context) (string, error) {
	if s.client == nil {
		return "", errNotConnected
	}
	return s.client.Info(ctx).Result()
}

// SlowLog reads the server's slow log and its total length.
func (s *Store) SlowLog(ctx context.Context, count int64) ([]redis.SlowLog, int64, error) {
	if s.client == nil {
		return nil, 0, errNotConnected
	}
	total, err := s.client.SlowLogLen(ctx).Result()
	if err != nil {
		return nil, 0, err
	}
	entries, err := s.client.SlowLogGet(ctx, count).Result()
	if err != nil {
		return nil, total, err
	}
	return entries, total, nil
}

// SlowLogReset empties the server's slow log.
func (s *Store) SlowLogReset(ctx context.Context) error {
	if s.client == nil {
		return errNotConnected
	}
	return s.client.SlowLogReset(ctx).Err()
}

// Do runs an arbitrary command for the CLI.
func (s *Store) Do(ctx context.Context, args ...string) (any, error) {
	if s.client == nil {
		return nil, errNotConnected
	}
	cmdArgs := make([]any, len(args))
	for i, a := range args {
		cmdArgs[i] = a
	}
	return s.client.Do(ctx, cmdArgs...).Result()
}

// Subscribe opens a pub/sub connection, or a pattern subscription.
func (s *Store) Subscribe(ctx context.Context, patterns bool, channels ...string) *redis.PubSub {
	if s.client == nil {
		return nil
	}
	if patterns {
		return s.client.PSubscribe(ctx, channels...)
	}
	return s.client.Subscribe(ctx, channels...)
}

// Publish sends a message to a channel.
func (s *Store) Publish(ctx context.Context, channel, payload string) (int64, error) {
	if s.client == nil {
		return 0, errNotConnected
	}
	return s.client.Publish(ctx, channel, payload).Result()
}

// ExportValue reads a complete key for CSV export on the background worker.
func (s *Store) ExportValue(ctx context.Context, key string) (Value, error) {
	if s.client == nil {
		return Value{}, errNotConnected
	}
	v := Value{TTL: -1}
	typ, err := s.client.Type(ctx, key).Result()
	if err != nil {
		return v, err
	}
	if typ == "none" {
		return v, fmt.Errorf("key %q does not exist", key)
	}
	if ttl, err := s.client.TTL(ctx, key).Result(); err == nil {
		v.TTL = ttl
	}
	if mem, err := s.client.MemoryUsage(ctx, key).Result(); err == nil {
		v.Memory = mem
	}
	switch typ {
	case "string":
		v.Type = "string"
		v.Str, err = s.client.Get(ctx, key).Result()
		v.Size = int64(len(v.Str))
	case "list":
		v.Type = "list"
		v.Size, err = s.client.LLen(ctx, key).Result()
		if err == nil {
			v.Items, err = s.client.LRange(ctx, key, 0, -1).Result()
		}
	case "hash":
		v.Type = "hash"
		v.Size, err = s.client.HLen(ctx, key).Result()
		if err == nil {
			var m map[string]string
			m, err = s.client.HGetAll(ctx, key).Result()
			if err == nil {
				for field, value := range m {
					v.Pairs = append(v.Pairs, [2]string{field, value})
				}
				sort.Slice(v.Pairs, func(i, j int) bool { return v.Pairs[i][0] < v.Pairs[j][0] })
			}
		}
	case "set":
		v.Type = "set"
		v.Size, err = s.client.SCard(ctx, key).Result()
		if err == nil {
			v.Items, err = s.client.SMembers(ctx, key).Result()
			sort.Strings(v.Items)
		}
	case "zset":
		v.Type = "zset"
		v.Size, err = s.client.ZCard(ctx, key).Result()
		if err == nil {
			var zs []redis.Z
			zs, err = s.client.ZRangeWithScores(ctx, key, 0, -1).Result()
			for _, z := range zs {
				v.Pairs = append(v.Pairs, [2]string{fmt.Sprint(z.Member), formatFloat(z.Score)})
			}
		}
	case "stream":
		v.Type = "stream"
		v.Size, err = s.client.XLen(ctx, key).Result()
		if err == nil {
			if info, infoErr := s.client.XInfoStream(ctx, key).Result(); infoErr == nil {
				v.LastID = info.LastGeneratedID
				v.Groups = info.Groups
			}
			var msgs []redis.XMessage
			msgs, err = s.client.XRange(ctx, key, "-", "+").Result()
			for _, m := range msgs {
				fields := make([]string, 0, len(m.Values))
				for field := range m.Values {
					fields = append(fields, field)
				}
				sort.Strings(fields)
				entry := StreamEntry{ID: m.ID}
				for _, field := range fields {
					entry.Fields = append(entry.Fields, [2]string{field, fmt.Sprint(m.Values[field])})
				}
				v.Stream = append(v.Stream, entry)
			}
		}
	case "ReJSON-RL", "rejson-rl", "json":
		v.Type = "json"
		v.Str, err = s.JSONGet(ctx, key)
		v.Size = int64(len(v.Str))
	default:
		err = fmt.Errorf("type %q is not supported for export", typ)
	}
	return v, err
}

func scanCommand(ctx context.Context, client redis.Cmdable, cursor uint64, pattern string, count int64, typ string) ([]string, uint64, error) {
	if typ == "" {
		return client.Scan(ctx, cursor, pattern, count).Result()
	}
	keys, next, err := client.ScanType(ctx, cursor, pattern, count, typ).Result()
	if err == nil || (!strings.Contains(strings.ToLower(err.Error()), "syntax") && !strings.Contains(strings.ToLower(err.Error()), "unknown")) {
		return keys, next, err
	}
	// Redis before 6 has no SCAN TYPE. Filter this page without changing its cursor.
	keys, next, err = client.Scan(ctx, cursor, pattern, count).Result()
	if err != nil {
		return nil, 0, err
	}
	out := keys[:0]
	for _, key := range keys {
		actual, e := client.Type(ctx, key).Result()
		if e != nil {
			return nil, 0, e
		}
		if actual == typ {
			out = append(out, key)
		}
	}
	return out, next, nil
}
