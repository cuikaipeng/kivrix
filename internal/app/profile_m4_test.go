package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestProfileConfigModes(t *testing.T) {
	p := Profile{
		Name: "Prod", Mode: "sentinel",
		SentinelAddrs: []string{"s1:26379", "s2:26379"}, MasterName: "mymaster",
		SentinelUser: "sentinel-user", Username: "redis-user", DB: 2,
		UseTLS: true, TLS: TLSConfig{CA: "ca.pem", Cert: "cert.pem", Key: "key.pem", ServerName: "redis.example"},
		Proxy: ProxyConfig{Type: "socks5", Addr: "proxy:1080", Username: "px"},
		SSH:   SSHConfig{Enabled: true, Addr: "bastion:22", User: "ops", Agent: true},
	}
	cfg := p.config(SecretSet{Redis: "r", SSH: "s", SSHPassphrase: "sp", Sentinel: "sen", Proxy: "pp"})
	if cfg.mode() != "sentinel" || cfg.MasterName != "mymaster" || cfg.SentinelPassword != "sen" {
		t.Fatalf("sentinel %+v", cfg)
	}
	if cfg.TLS.ServerName != "redis.example" || !cfg.UseTLS || cfg.Proxy.Password != "pp" {
		t.Fatalf("tls/proxy %+v", cfg)
	}
	if cfg.SSHPassphrase != "sp" || !cfg.SSH.Agent {
		t.Fatalf("ssh %+v", cfg)
	}
	p.Mode = "cluster"
	p.ClusterAddrs = []string{"c1:7000", "c2:7000"}
	p.DB = 3
	cfg = p.config(SecretSet{})
	if cfg.mode() != "cluster" || len(cfg.ClusterAddrs) != 2 || cfg.DB != 3 {
		t.Fatalf("cluster %+v", cfg)
	}
}

func TestValidateProfile(t *testing.T) {
	cases := []struct {
		p    Profile
		want string
	}{
		{Profile{}, "Address"},
		{Profile{Mode: "sentinel"}, "sentinel address"},
		{Profile{Mode: "sentinel", SentinelAddrs: []string{"s:1"}}, "master name"},
		{Profile{Mode: "cluster"}, "cluster node address"},
		{Profile{Addr: "a:1", UseTLS: true, TLS: TLSConfig{Cert: "c"}}, "certificate and key"},
		{Profile{Addr: "a:1", Proxy: ProxyConfig{Type: "http"}}, "proxy address"},
		{Profile{Addr: "a:1", SSH: SSHConfig{Enabled: true}}, "SSH address"},
	}
	for _, tc := range cases {
		if got := validateProfile(tc.p); !strings.Contains(got, tc.want) {
			t.Fatalf("%+v -> %q, want %q", tc.p, got, tc.want)
		}
	}
	if got := validateProfile(Profile{Addr: "a:1"}); got != "" {
		t.Fatalf("valid profile: %q", got)
	}
}

func TestConnectionFormModes(t *testing.T) {
	a := newApp()
	a.form.reset()
	a.form.mode = "Sentinel"
	a.form.sentinelAddrs = "s1:26379, s2:26379"
	a.form.masterName = "mymaster"
	a.form.tls = true
	a.form.tlsCA = "~/ca.pem"
	a.form.proxy = "SOCKS5"
	a.form.proxyAddr = "proxy:1080"
	a.form.ssh = true
	a.form.sshAddr = "bastion:22"
	a.form.sshPassphrase = "secret"
	a.form.sshAgent = true
	a.form.showAdvanced = true
	tt := ui.NewTester(a.view, 1180, 1000)
	for _, want := range []string{
		"Mode", "Sentinel Addresses", "Master Name", "Sentinel Username",
		"CA Bundle", "Client Certificate", "Server Name (SNI)",
		"Proxy Address", "Proxy Password", "SSH Address", "Key Passphrase",
		"Use SSH agent",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestConnectionRowModeBadges(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{
		{ID: "s", Name: "Sentinel", Mode: "sentinel", SentinelAddrs: []string{"s:26379"}, MasterName: "mymaster", Proxy: ProxyConfig{Type: "http", Addr: "p:8080"}},
		{ID: "c", Name: "Cluster", Mode: "cluster", ClusterAddrs: []string{"c:7000"}},
	}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"SENT", "CLUSTER", "PROXY", "sentinel · mymaster", "cluster · c:7000"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestProfileSecretsAllKinds(t *testing.T) {
	clear(fakeSecrets)
	a := newApp()
	a.profilesPath = t.TempDir() + "/profiles.json"
	a.form.reset()
	a.form.mode = "Sentinel"
	a.form.sentinelAddrs = "s:26379"
	a.form.masterName = "mymaster"
	a.form.sentinelPass = "sent"
	a.form.password = "redis"
	a.form.proxy = "HTTP"
	a.form.proxyAddr = "p:8080"
	a.form.proxyPassword = "proxy"
	a.form.ssh = true
	a.form.sshAddr = "bastion:22"
	a.form.sshPassword = "ssh"
	a.form.sshPassphrase = "phrase"
	a.form.sshKey = "~/.ssh/id_ed25519"
	a.saveProfile()
	if a.connErr.key != "" {
		t.Fatal(a.connErr)
	}
	id := a.selProfile
	for _, key := range []string{"redis", "sentinel", "proxy", "ssh", "ssh-passphrase"} {
		if fakeSecrets["profile/"+id+"/"+key] == "" {
			t.Fatalf("missing %s in %v", key, fakeSecrets)
		}
	}
	// Turning off optional features removes their secrets.
	a.form.mode = "Standalone"
	a.form.addr = "127.0.0.1:6379"
	a.form.ssh = false
	a.form.proxy = "None"
	a.saveProfile()
	for _, key := range []string{"sentinel", "proxy", "ssh", "ssh-passphrase"} {
		if _, ok := fakeSecrets["profile/"+id+"/"+key]; ok {
			t.Fatalf("%s left in the secret store", key)
		}
	}
}

func TestConnConfigLabelModes(t *testing.T) {
	if got := (ConnConfig{Mode: "sentinel", MasterName: "mymaster"}).label(); got != "mymaster" {
		t.Fatalf("sentinel label %q", got)
	}
	if got := (ConnConfig{Mode: "cluster", ClusterAddrs: []string{"c:7000"}}).label(); got != "c:7000" {
		t.Fatalf("cluster label %q", got)
	}
	if got := (ConnConfig{Addr: "a:6379"}).label(); got != "a:6379" {
		t.Fatalf("standalone label %q", got)
	}
}

func TestClusterSessionDoesNotSwitchDB(t *testing.T) {
	a := newApp()
	s := newSession(a, Profile{ID: "c", Name: "Cluster"}, Store{cfg: ConnConfig{Mode: "cluster"}, kind: "cluster"}, "")
	s.db = 0
	s.dbChoice = "db0"
	s.dbChoices = []string{"db0"}
	s.switchDB(1)
	if s.db != 0 || s.dbSwitching {
		t.Fatalf("db %d switching %v", s.db, s.dbSwitching)
	}
	if !s.store.IsCluster() {
		t.Fatal("store not cluster")
	}
}
