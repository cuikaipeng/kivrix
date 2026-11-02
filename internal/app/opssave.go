package app

// Saving and copying text from the server tools.

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *App) copyOpsText(c *ui.Context, s *session, text, label string) {
	if text == "" {
		s.setStatus("Nothing to copy")
		return
	}
	c.Services().WriteClipboard(text)
	s.setStatus("Copied %s", msg(label))
}

func (a *App) saveOpsText(s *session, title, defaultName, content string) {
	if content == "" {
		s.setStatus("Nothing to save")
		return
	}
	if s.opsSaving {
		return
	}
	s.opsSaving = true
	dialogTitle := a.tr(title)
	filterName := a.tr("Text files")
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       dialogTitle,
			DefaultPath: defaultName,
			Filters:     []mygo.FileFilter{{Name: filterName, Extensions: []string{"txt", "log"}}},
		})
		if err == nil && path != "" {
			err = writePrivateFile(path, []byte(content))
		}
		a.update(func() {
			defer func() { s.opsSaving = false }()
			if err != nil {
				s.setStatusErr(err)
				return
			}
			if path == "" {
				s.setStatus("Save canceled")
				return
			}
			s.setStatus("Saved %s", msg(title))
		})
	}()
}
