package app

import (
	"errors"

	"github.com/egoist/mygo/ui"
)

type editGuard struct {
	session *session
	next    func()
	err     string
}

func (s *session) hasEdits() bool {
	return s != nil && !s.closed && (s.valueSaving || s.editorModified())
}

// Continuations run only after an explicit discard or a successful save of the
// current buffer version. Cancel and failed saves leave the editor untouched.
func (a *App) protectEdits(s *session, next func()) {
	if !s.hasEdits() {
		next()
		return
	}
	if a.editGuard == nil {
		a.editGuard = &editGuard{session: s, next: next}
	}
}

func (a *App) protectAllEdits(next func()) {
	for _, s := range a.sessions {
		if s.hasEdits() {
			a.protectEdits(s, func() { a.protectAllEdits(next) })
			return
		}
	}
	next()
}

func (s *session) markEditorsSaved() {
	if s.strEditor.Buffer != nil {
		s.strEditor.Buffer.Reset(s.val.Str)
		s.strEditor.Buffer.MarkSaved()
	}
	if s.decodedEditor.Buffer != nil {
		s.decodedEditor.Buffer.MarkSaved()
	}
	if s.encodedEditorActive() {
		s.prepareDecodedEditor(s.val.Str)
	}
}

func (a *App) viewEditGuard(c *ui.Context) {
	g := a.editGuard
	if g == nil {
		return
	}
	s, pal := g.session, a.paletteFor(c)
	if s.closed {
		a.editGuard = nil
		return
	}
	open := true
	ui.Modal(c.Key("unsaved-edits"), &open, func() {
		ui.Column(c).Width(430).Padding(8).Gap(12).Children(func() {
			ui.Text(c, a.tr("Save your changes?")).FontSize(17).FontWeight(650)
			ui.Text(c, s.connectionContext()).FontSize(12).TextColor(pal.muted)
			ui.Text(c, a.tr("%s has unsaved changes.", displayKey(s.valKey))).FontSize(13)
			if g.err != "" {
				ui.Text(c, a.tr(g.err)).TextColor(pal.danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				textButton(c, "edit-cancel", a.tr("Cancel"), false, false, pal).OnClick(func() { a.editGuard = nil })
				textButton(c, "edit-discard", a.tr("Discard"), false, true, pal).Disabled(s.valueSaving).OnClick(func() {
					s.markEditorsSaved()
					a.editGuard = nil
					g.next()
				})
				label := a.tr("Save and continue")
				if s.valueSaving {
					label = a.tr("Saving…")
				}
				textButton(c, "edit-save", label, true, false, pal).Disabled(s.valueSaving || s.cfg.ReadOnly).OnClick(func() {
					if !s.editorModified() {
						a.editGuard = nil
						g.next()
						return
					}
					g.err = ""
					s.saveString(func(err error) {
						if a.editGuard != g {
							return
						}
						if err != nil {
							g.err = err.Error()
							return
						}
						if s.editorModified() {
							g.err = "The value changed during saving. Save again to continue."
							return
						}
						a.editGuard = nil
						g.next()
					})
				})
			})
		})
	})
	if !open {
		a.editGuard = nil
	}
}

var errSaveObsolete = errors.New("The edited value is no longer open.")
