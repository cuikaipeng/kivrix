package app

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
)

// English phrases are stable message IDs. Values are ICU MessageFormat, so
// translations can reorder named arguments and use their own plural rules.
//
//go:embed locales/*.json
var localeFiles embed.FS

type translations struct {
	catalog *i18n.Catalog
	args    map[string][]i18n.Arg
}

var texts = loadCatalog()

func loadCatalog() *translations {
	files, err := fs.Sub(localeFiles, "locales")
	if err != nil {
		panic(err)
	}
	t, err := readTranslations(files)
	if err != nil {
		panic(err)
	}
	i18n.Register(t.catalog)
	return t
}

func readTranslations(files fs.FS) (*translations, error) {
	c, err := i18n.NewCatalog(files, "en")
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(files, "en.json")
	if err != nil {
		return nil, err
	}
	var source map[string]string
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, err
	}
	args := make(map[string][]i18n.Arg, len(source))
	for key := range source {
		if m, _, ok := c.Message("en", key); ok {
			args[key] = m.Args()
		}
	}
	return &translations{c, args}, nil
}

func normalizeLanguage(value string) string {
	switch value {
	case "English":
		return "en"
	case "中文", "简体中文":
		return "zh-Hans"
	case "", "System", "Follow System", "system":
		return "system"
	}
	if tag := i18n.Normalize(value); slices.Contains(texts.catalog.Languages(), tag) {
		return tag
	}
	return "system"
}

func resolveLanguage(value string, preferred []string) string {
	if tag := normalizeLanguage(value); tag != "system" {
		return tag
	}
	if tag := i18n.Match(preferred, texts.catalog.Languages()); tag != "" {
		return tag
	}
	return "en"
}

func (a *App) locale() string {
	if a == nil {
		return "en"
	}
	if a.win != nil {
		return mygo.App.Locale()
	}
	// Headless views use the same matching policy without changing global state.
	return resolveLanguage(a.prefs.Language, nil)
}

func languageOptions() []string { return append([]string{"system"}, texts.catalog.Languages()...) }

func (a *App) languageLabel(tag string) string {
	if normalizeLanguage(tag) == "system" {
		return a.tr("Follow System")
	}
	return i18n.Autonym(normalizeLanguage(tag))
}

func (a *App) tr(key string, values ...any) string {
	return texts.format(a.locale(), key, values...)
}

func (t *translations) format(locale, key string, values ...any) string {
	var args map[string]any
	if len(values) > 0 {
		args = make(map[string]any, len(values))
		for i, arg := range t.args[key] {
			if i >= len(values) {
				break
			}
			value := values[i]
			if m, ok := value.(message); ok {
				value = t.format(locale, m.key, m.args...)
			}
			args[arg.Name] = value
		}
	}
	return t.catalog.Format(locale, key, args)
}

// Keep templates and raw arguments in state; resolve them when rendering, so
// existing messages change language along with the rest of the window.
type message struct {
	key  string
	args []any
}

func msg(key string, args ...any) message { return message{key, args} }
func (m message) text(a *App) string      { return a.tr(m.key, m.args...) }

type messageError struct{ message }

func localError(key string, args ...any) error { return messageError{msg(key, args...)} }
func (e messageError) Error() string           { return e.text(nil) }
func (e messageError) Unwrap() error {
	for _, arg := range e.args {
		if err, ok := arg.(error); ok {
			return err
		}
	}
	return nil
}

func errorMessage(err error) message {
	if errors.Is(err, errReadOnly) {
		return msg(errReadOnly.Error())
	}
	if e, ok := err.(messageError); ok {
		return e.message
	}
	// Redis and OS diagnostics are raw data, not translation IDs.
	return msg("%s", fmt.Sprint(err))
}
