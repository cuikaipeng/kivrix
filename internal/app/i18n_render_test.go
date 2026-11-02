package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestChineseStatusAndSlowLog(t *testing.T) {
	a := newApp()
	a.prefs.Language = "中文"
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "status"
	s.statusInfo = parseInfo("# Server\nredis_version:7.2\nredis_mode:standalone\n# Clients\nconnected_clients:2\n# Memory\nused_memory:1024\n# Stats\ninstantaneous_input_kbps:1\ninstantaneous_output_kbps:2\n")
	s.statusUpdated = time.Now()
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"服务器状态", "服务器", "版本", "模式", "网络", "客户端"} {
		if !tt.HasText(want) {
			t.Fatalf("status missing %q: %q", want, tt.Texts())
		}
	}

	sl := newApp()
	sl.prefs.Language = "中文"
	ss := testSession(sl, ConnConfig{Addr: "127.0.0.1:6379"})
	ss.tool = "slowlog"
	ss.slowTotal = 1
	ss.slowEntries = []slowEntry{{at: time.Now(), duration: time.Millisecond, command: "GET", client: "app"}}
	stt := ui.NewTester(sl.view, 1180, 760)
	for _, want := range []string{"慢日志", "时间", "耗时", "客户端", "命令"} {
		if !stt.HasText(want) {
			t.Fatalf("slowlog missing %q: %q", want, stt.Texts())
		}
	}
}

func TestChineseValueAndPreferences(t *testing.T) {
	a := newApp()
	a.prefs.Language = "中文"
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "user:1"
	s.val = Value{Type: "hash", Pairs: [][2]string{{"name", "Ada"}}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"值", "字段", "db0 (?)", "1 个字段"} {
		if !tt.HasText(want) {
			t.Fatalf("value missing %q: %q", want, tt.Texts())
		}
	}

	p := newApp()
	p.prefs.Language = "中文"
	p.prefsOpen = true
	p.prefsTab = settingsDecoders
	ptt := ui.NewTester(p.settingsView, 820, 620)
	for _, want := range []string{"常规", "解码器", "自定义解码器", "添加解码器"} {
		if !ptt.HasText(want) {
			t.Fatalf("prefs missing %q: %q", want, ptt.Texts())
		}
	}
}

func TestChineseDeleteDialog(t *testing.T) {
	a := newApp()
	a.prefs.Language = "中文"
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	a.askDeleteKey(s, "user:1")
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("删除 key “user:1”？") || !tt.HasText("取消") {
		t.Fatalf("dialog texts %q", tt.Texts())
	}
}

func TestChineseConnectionMode(t *testing.T) {
	a := newApp()
	a.prefs.Language = "中文"
	a.form.reset()
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("单机") {
		t.Fatalf("connection labels: %q", tt.Texts())
	}
	if err := tt.Click("单机"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("集群"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.form.mode != "Cluster" {
		t.Fatalf("mode %q", a.form.mode)
	}
}

func TestChineseAppearanceLabels(t *testing.T) {
	a := newApp()
	a.prefs.Language = "简体中文"
	a.prefsOpen = true
	a.prefsTab = settingsAppearance
	tt := ui.NewTester(a.settingsView, 820, 620)
	for _, want := range []string{"外观", "跟随系统", "浅色", "深色", "简体中文", "背景"} {
		if !tt.HasText(want) {
			t.Fatalf("appearance missing %q: %q", want, tt.Texts())
		}
	}
}
