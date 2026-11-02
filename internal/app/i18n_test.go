package app

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// This runs in ordinary go test and CI: malformed or incomplete translations
// must not silently become an English fallback in a shipped language.
func TestLocaleCatalogs(t *testing.T) {
	files, err := fs.Glob(localeFiles, "locales/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := localeFiles.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var entries map[string]string
			if err := json.Unmarshal(data, &entries); err != nil {
				t.Fatal(err)
			}
			locale := strings.TrimSuffix(filepath.Base(file), ".json")
			if entries["@@locale"] != locale {
				t.Fatalf("@@locale must match %s", locale)
			}
			for key, sourceArgs := range texts.args {
				value, ok := entries[key]
				if !ok || value == "" {
					t.Errorf("missing translation: %q", key)
					continue
				}
				m, err := i18n.ParseMessage(value)
				if err != nil {
					t.Errorf("%q: %v", key, err)
					continue
				}
				if !slices.Equal(argumentNames(sourceArgs), argumentNames(m.Args())) {
					t.Errorf("%q: placeholders %v differ from source %v", key, m.Args(), sourceArgs)
				}
				for _, arg := range m.Args() {
					if arg.Kind != "plural" && arg.Kind != "selectordinal" {
						continue
					}
					categories := i18n.PluralCategories(locale)
					if arg.Kind == "selectordinal" {
						categories = i18n.OrdinalCategories(locale)
					}
					for _, category := range categories {
						if !slices.Contains(m.Cases(arg.Name), category.String()) {
							t.Errorf("%q: %s lacks plural case %s", key, arg.Name, category)
						}
					}
				}
			}
			for key := range entries {
				if strings.HasPrefix(key, "@") {
					continue
				}
				if _, ok := texts.args[key]; !ok {
					t.Errorf("unknown key or invalid English message: %q", key)
				}
			}
		})
	}
}

func argumentNames(args []i18n.Arg) []string {
	names := make([]string, len(args))
	for i, arg := range args {
		names[i] = arg.Name
	}
	slices.Sort(names)
	return names
}

// Catch new literal IDs that have no source message, and the old pattern of
// formatting a translation with printf after the placeholders have changed.
func TestTranslationCallSites(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || call.Ellipsis.IsValid() {
				return true
			}
			var name string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if name != "tr" && name != "setStatus" && name != "msg" && name != "localError" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(literal.Value)
			if key == "" {
				return true
			}
			args, ok := texts.args[key]
			if !ok {
				t.Errorf("%s: unknown message %q", fset.Position(call.Pos()), key)
			} else if len(args) != len(call.Args)-1 {
				t.Errorf("%s: %q expects %d arguments, got %d", fset.Position(call.Pos()), key, len(args), len(call.Args)-1)
			}
			return true
		})
	}
}

func TestLocaleMatching(t *testing.T) {
	for _, tc := range []struct {
		language  string
		preferred []string
		want      string
	}{
		{"system", []string{"fr-FR", "zh-Hans-CN", "en"}, "zh-Hans"},
		{"system", []string{"zh-Hant-TW", "en"}, "en"},
		{"system", []string{"zh-Hans-CN", "en"}, "zh-Hans"},
		{"en", []string{"zh-Hans"}, "en"},
		{"中文", []string{"en"}, "zh-Hans"},
		{"system", nil, "en"},
	} {
		if got := resolveLanguage(tc.language, tc.preferred); got != tc.want {
			t.Errorf("%s, %v: got %s, want %s", tc.language, tc.preferred, got, tc.want)
		}
	}
}

func TestAdditionalLocaleAndFallback(t *testing.T) {
	fixture := fstest.MapFS{
		"en.json": {Data: []byte(`{"Greeting":"Hello, {name}","Keys":"{count, plural, one {# key} other {# keys}}","Fallback":"English"}`)},
		"de.json": {Data: []byte(`{"Greeting":"Hallo, {name}","Keys":"{count, plural, one {# Schlüssel} other {# Schlüssel}}"}`)},
	}
	tr, err := readTranslations(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tr.catalog.Languages(), []string{"en", "de"}) {
		t.Fatal(tr.catalog.Languages())
	}
	for _, tc := range []struct {
		locale, key string
		args        []any
		want        string
	}{
		{"de", "Greeting", []any{"Ada"}, "Hallo, Ada"},
		{"de", "Keys", []any{2}, "2 Schlüssel"},
		{"en", "Keys", []any{1}, "1 key"},
		{"en", "Keys", []any{2}, "2 keys"},
		{"de", "Fallback", nil, "English"},
		{"ja", "Greeting", []any{"Ada"}, "Hello, Ada"},
	} {
		if got := tr.format(tc.locale, tc.key, tc.args...); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.locale, tc.key, got, tc.want)
		}
	}
}

func TestTranslatedArguments(t *testing.T) {
	a := newApp()
	a.prefs.Language = "zh-Hans"
	if got := a.tr("Deleted %d keys under %s", 3, "user:"); got != "已删除 user: 下的 3 个 key" {
		t.Fatal(got)
	}
	// User data must remain verbatim, even when it looks like a message ID or ICU.
	if got := a.tr("Saved %s", "Browser {value} 100%"); got != "已保存 Browser {value} 100%" {
		t.Fatal(got)
	}
	if got := a.tr("Copied %s", msg("monitor log")); got != "已复制 监视日志" {
		t.Fatal(got)
	}
	if got := a.tr("Reads the value on stdin. Only run programs you trust."); !strings.Contains(got, "标准输入") {
		t.Fatal(got)
	}
	cause := errors.New("Ready")
	if got := errorMessage(cause).text(a); got != "Ready" {
		t.Fatal(got)
	}
	err := localError("Could not read the saved password: %s", cause)
	if !errors.Is(err, cause) || errorMessage(err).text(a) != "无法读取保存的密码：Ready" {
		t.Fatal(err)
	}
}

func TestLanguageSwitchRendersExistingMessages(t *testing.T) {
	a := newApp()
	a.prefs.Language = "en"
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.setStatus("Found %d keys", 3)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, tc := range []struct{ locale, text string }{
		{"en", "Found 3 keys"}, {"zh-Hans", "找到 3 个 key"}, {"en", "Found 3 keys"},
	} {
		a.prefs.Language = tc.locale
		tt.Frame()
		if !tt.HasText(tc.text) {
			t.Fatalf("missing %q: %v", tc.text, tt.Texts())
		}
	}
	a.updateStatus = msg("Version %s is available", "2.0.0")
	a.connTestResult = msg("Connected · %s", "Redis 7.2")
	a.prefs.Language = "zh-Hans"
	if a.updateStatus.text(a) != "发现新版本 2.0.0" || a.connTestResult.text(a) != "已连接 · Redis 7.2" {
		t.Fatal("stored results did not follow language")
	}
	a.prefs.Language = "en"
	if a.updateStatus.text(a) != "Version 2.0.0 is available" {
		t.Fatal(a.updateStatus.text(a))
	}
}

func TestChineseValidationAndLanguageSelection(t *testing.T) {
	a := newApp()
	a.prefs.Language = "zh-Hans"
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey, s.val = "user:1", Value{Type: "hash"}
	a.openRowAdd(s)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("请输入字段名称。") {
		t.Fatal(tt.Texts())
	}
	a.prefs.Language = "en"
	tt.Frame()
	if !tt.HasText("A field name is required.") {
		t.Fatal(tt.Texts())
	}
	a.rowOpen = false
	a.prefsOpen, a.prefsTab = true, settingsAppearance
	prefs := ui.NewTester(a.settingsView, 820, 620)
	if err := prefs.Click("English"); err != nil {
		t.Fatal(err)
	}
	prefs.Frame()
	if err := prefs.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	prefs.Frame()
	if a.prefs.Language != "zh-Hans" || !prefs.HasText("外观") {
		t.Fatalf("language %q, texts %v", a.prefs.Language, prefs.Texts())
	}
	saveParityView(t, prefs, "i18n-settings-zh")
	if err := prefs.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	prefs.Frame()
	if err := prefs.Click("English"); err != nil {
		t.Fatal(err)
	}
	prefs.Frame()
	if a.prefs.Language != "en" || !prefs.HasText("Appearance") {
		t.Fatalf("language %q, texts %v", a.prefs.Language, prefs.Texts())
	}
	saveParityView(t, prefs, "i18n-settings-en")
}
