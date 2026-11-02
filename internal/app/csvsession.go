package app

// CSV actions for one session.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// csvKeys is the selection, or every key the browser has loaded.
func (s *session) csvKeys() []string {
	keys := s.selectedKeys()
	if len(keys) == 0 {
		for path, n := range s.keys.byPath {
			if n.isKey {
				keys = append(keys, path)
			}
		}
		sort.Strings(keys)
	}
	return keys
}

// exportCSV writes keys to path and reports the result in the status line.
func (s *session) exportCSV(path string, keys []string, includeTTL bool) {
	if s == nil || s.closed || s.ioBusy {
		return
	}
	s.ioBusy = true
	s.setStatus("Exporting %d keys…", len(keys))
	st := s.store
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	s.ioCancel = cancel
	report := s.beginTransfer("Exporting", len(keys))
	go func() {
		defer cancel()
		n, skipped, err := exportSessionCSVWithTTL(ctx, &st, path, keys, includeTTL, report)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.endTransfer()
			if errors.Is(err, context.Canceled) {
				s.setStatus("Export canceled: %d exported, %d skipped", n, skipped)
				return
			}
			if err != nil {
				s.setStatusErr(localError("exported %d keys, skipped %d: %w", n, skipped, err))
				return
			}
			if skipped > 0 {
				s.setStatus("Exported %d keys · skipped %d", n, skipped)
			} else {
				s.setStatus("Exported %d keys", n)
			}
		})
	}()
}

// importCSV reads keys from path and refreshes the keyspace.
func (s *session) importCSV(path string, opts CSVImportOptions) {
	if !s.writable() {
		return
	}
	s.app.protectEdits(s, func() { s.importCSVNow(path, opts) })
}

func (s *session) importCSVNow(path string, opts CSVImportOptions) {
	if s == nil || s.closed || s.ioBusy {
		return
	}
	s.ioBusy = true
	s.setStatus("Importing CSV…")
	st := s.store
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s.ioCancel = cancel
	report := s.beginTransfer("Importing", 0)
	go func() {
		defer cancel()
		n, skipped, err := importSessionCSVWithOptions(ctx, &st, path, opts, report)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.endTransfer()
			if n > 0 && opts.Reload {
				s.startScan()
				s.refreshDatabases()
			}
			if errors.Is(err, context.Canceled) {
				s.setStatus("Import canceled: %d imported, %d skipped. Completed writes remain.", n, skipped)
				return
			}
			if err != nil {
				s.setStatusErr(localError("imported %d keys, skipped %d: %w", n, skipped, err))
				return
			}
			if skipped > 0 {
				s.setStatus("Imported %d keys · skipped %d", n, skipped)
			} else {
				s.setStatus("Imported %d keys", n)
			}
		})
	}()
}

// exportSessionCSV opens the export options, then a save dialog.
func (a *App) exportSessionCSV(s *session) {
	if s == nil || s.closed || s.ioBusy {
		return
	}
	keys := s.csvKeys()
	if len(keys) == 0 {
		s.setStatus("No loaded keys to export")
		return
	}
	a.csvExportID = s.id
	a.csvExportKeys = keys
	a.csvExportTTL = true
	a.csvExportOpen = true
}

func (a *App) viewCSVExport(c *ui.Context) {
	if !a.csvExportOpen {
		return
	}
	pal := a.paletteFor(c)
	ui.Modal(c.Key("csv-export"), &a.csvExportOpen, func() {
		ui.Column(c).Width(400).Padding(4).Gap(10).Children(func() {
			ui.Text(c, a.tr("Export CSV")).FontSize(15).FontWeight(650).TextColor(pal.text)
			countLabel := a.tr("%d keys", len(a.csvExportKeys))
			if len(a.csvExportKeys) == 1 {
				countLabel = a.tr("1 key")
			}
			ui.Text(c, countLabel).FontSize(11).TextColor(pal.faint)
			ui.Checkbox(c, &a.csvExportTTL, a.tr("Include TTL"))
			ui.Text(c, a.tr("Values are written as plain text.")).FontSize(10.5).TextColor(pal.faint)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "csv-export-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					a.csvExportOpen = false
				}
				if textButton(c, "csv-export-choose", a.tr("Choose file…"), true, false, pal).Clicked() {
					a.csvExportOpen = false
					a.pickCSVExport(a.sessionByID(a.csvExportID), a.csvExportKeys, a.csvExportTTL)
				}
			})
		})
	})
}

func (a *App) pickCSVExport(s *session, keys []string, includeTTL bool) {
	if s == nil || s.closed || s.ioBusy {
		return
	}
	s.ioBusy = true
	s.setStatus("Choose a file for the export…")
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       a.tr("Export keys"),
			DefaultPath: "kivrix.csv",
			Filters:     []mygo.FileFilter{{Name: a.tr("CSV files"), Extensions: []string{"csv"}}},
		})
		a.update(func() {
			if s.closed {
				return
			}
			if err != nil {
				s.ioBusy = false
				s.setStatusErr(err)
				return
			}
			if path == "" {
				s.ioBusy = false
				s.setStatus("Export canceled")
				return
			}
			s.ioBusy = false
			s.exportCSV(path, keys, includeTTL)
		})
	}()
}

// importSessionCSV opens the import options; choosing a file starts the
// actual import with those options.
func (a *App) importSessionCSV(s *session) {
	if !s.writable() {
		return
	}
	if s == nil || s.closed || s.ioBusy {
		return
	}
	a.csvImportID = s.id
	a.csvImportOpts = defaultCSVImportOptions()
	a.csvImportTTL = 3600
	a.csvImportOpen = true
}

func (a *App) viewCSVImport(c *ui.Context) {
	if !a.csvImportOpen {
		return
	}
	pal := a.paletteFor(c)
	s := a.sessionByID(a.csvImportID)
	ui.Modal(c.Key("csv-import"), &a.csvImportOpen, func() {
		ui.Column(c).Width(430).Padding(4).Gap(10).Children(func() {
			ui.Text(c, a.tr("Import CSV")).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Field(c, a.tr("If a key exists"), func() {
				ui.Select(c, &a.csvImportOpts.Conflict, []string{"Skip", "Overwrite"}).Width(190)
			})
			ui.Field(c, a.tr("TTL"), func() {
				ui.Select(c, &a.csvImportOpts.TTLMode, []string{"Include", "Ignore", "Custom"}).Width(190)
			})
			if strings.EqualFold(a.csvImportOpts.TTLMode, "Custom") {
				ui.Field(c, a.tr("Custom TTL (seconds)"), func() {
					ui.NumberInput(c, &a.csvImportTTL, 0, 31536000, 1).Width(160)
				})
			}
			ui.Checkbox(c, &a.csvImportOpts.Reload, a.tr("Reload keys after import"))
			if strings.EqualFold(a.csvImportOpts.Conflict, "Overwrite") {
				ui.Text(c, a.tr("Existing keys are deleted before they are imported.")).
					FontSize(10.5).TextColor(pal.warning).LineHeight(1.3)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "csv-import-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					a.csvImportOpen = false
				}
				if textButton(c, "csv-import-choose", a.tr("Choose file…"), true, false, pal).Clicked() {
					opts := a.csvImportOpts
					opts.CustomTTL = int64(a.csvImportTTL)
					a.csvImportOpen = false
					a.pickCSVImport(s, opts)
				}
			})
		})
	})
}

func (a *App) pickCSVImport(s *session, opts CSVImportOptions) {
	if s == nil || s.closed || s.ioBusy {
		return
	}
	s.ioBusy = true
	s.setStatus("Choose a CSV file to import…")
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   a.tr("Import keys"),
			Filters: []mygo.FileFilter{{Name: a.tr("CSV files"), Extensions: []string{"csv"}}},
		})
		a.update(func() {
			if s.closed {
				return
			}
			if err != nil {
				s.ioBusy = false
				s.setStatusErr(err)
				return
			}
			if len(paths) == 0 {
				s.ioBusy = false
				s.setStatus("Import canceled")
				return
			}
			s.ioBusy = false
			s.importCSV(paths[0], opts)
		})
	}()
}
