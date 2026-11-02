package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *session) namespacePrefix(path string) string {
	separator := s.cfg.Separator
	if separator == "" {
		separator = keyTreeDelim
	}
	return path + separator
}

func (a *App) slowResetContext(s *session) string {
	text := a.tr("Every slow log entry on the server will be removed. This cannot be undone.")
	if s != nil {
		text = s.connectionContext() + "\n\n" + text
	}
	return text
}

func (a *App) viewDeleteConfirmation(c *ui.Context) {
	p := a.pending
	s := a.sessionByID(p.sessionID)
	if s == nil || s.closed {
		a.confirmOpen = false
		return
	}
	title, message, phrase := "", "", ""
	switch {
	case p.flush:
		title = a.tr("Flush db%d?", s.db)
		message = a.tr("Every key in this database will be deleted. This cannot be undone.")
		phrase = fmt.Sprintf("db%d", s.db)
	case p.namespace:
		title = a.tr("Delete namespace “%s”?", p.path)
		message = a.tr("Every key whose name starts with “%s” on the server will be deleted. This cannot be undone.", s.namespacePrefix(p.path))
		phrase = "DELETE"
	case len(p.keys) > 1:
		title = a.tr("Delete %d keys?", len(p.keys))
		message = a.tr("The chosen keys will be deleted. This cannot be undone.")
		if len(p.keys) >= 10 || s.cfg.Environment == "Production" {
			phrase = "DELETE"
		}
	default:
		key := p.path
		if len(p.keys) == 1 {
			key = p.keys[0]
		}
		title = a.tr("Delete key “%s”?", displayKey(key))
		message = a.tr("This cannot be undone.")
	}
	pal := a.paletteFor(c)
	ui.Modal(c.Key("delete-confirmation"), &a.confirmOpen, func() {
		ui.Column(c).Width(430).Padding(8).Gap(12).Children(func() {
			ui.Text(c, title).FontSize(17).FontWeight(650).TextColor(pal.text)
			ui.Text(c, s.connectionContext()).FontSize(12).TextColor(pal.danger)
			ui.Text(c, message).FontSize(13).TextColor(pal.muted)
			if phrase != "" {
				ui.Field(c, a.tr("Type %s to confirm", phrase), func() {
					ui.TextInput(c, &a.confirmPhrase).Label(a.tr("Confirmation text")).AutoFocus().Grow(1)
				})
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				textButton(c, "delete-cancel", a.tr("Cancel"), false, false, pal).OnClick(func() { a.confirmOpen = false; a.pending = pendingDelete{} })
				label := "Delete"
				if p.flush {
					label = "Flush"
				}
				textButton(c, "delete-confirm", a.tr(label), false, true, pal).
					Disabled(s.cfg.ReadOnly || (phrase != "" && strings.TrimSpace(a.confirmPhrase) != phrase)).OnClick(func() {
					a.confirmOpen = false
					a.protectEdits(s, func() {
						if !s.writable() {
							return
						}
						switch {
						case p.flush:
							s.flushDB(func(err error) {
								if err != nil {
									s.setStatusErr(err)
								}
							})
						case len(p.keys) > 0:
							s.deleteKeys(p.keys)
						case p.namespace:
							s.deleteNamespace(p.path)
						default:
							s.deleteKey(p.path)
						}
					})
				})
			})
		})
	})
	if !a.confirmOpen {
		a.pending = pendingDelete{}
	}
}
