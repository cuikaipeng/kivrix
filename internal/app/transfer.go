package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

type transferProgress struct{ done, skipped int }

// Reuse the bounded event queue: thousands of records still produce at most
// ten UI updates per second, with only one callback pending at a time.
func (s *session) beginTransfer(label string, total int) func(int, int) {
	s.ioStarted, s.ioElapsed = time.Now(), 0
	s.ioLabel, s.ioTotal, s.ioDone, s.ioSkipped = label, total, 0, 0
	q := &eventQueue[transferProgress]{limit: 1, post: s.app.update, apply: func(items []transferProgress, _ int) {
		if !s.closed && s.ioBusy && len(items) > 0 {
			p := items[len(items)-1]
			s.ioDone, s.ioSkipped = p.done, p.skipped
			s.ioElapsed = time.Since(s.ioStarted)
		}
	}}
	s.ioProgress = q
	s.tickTransfer()
	return func(done, skipped int) { q.push(transferProgress{done, skipped}) }
}

func (s *session) tickTransfer() {
	q := s.ioProgress
	s.ioTimer = time.AfterFunc(time.Second, func() {
		s.app.update(func() {
			if !s.closed && s.ioBusy && s.ioProgress == q && q != nil {
				s.ioElapsed = time.Since(s.ioStarted)
				s.tickTransfer()
			}
		})
	})
}

func (s *session) endTransfer() {
	if s.ioCancel != nil {
		s.ioCancel()
	}
	if s.ioTimer != nil {
		s.ioTimer.Stop()
		s.ioTimer = nil
	}
	s.ioProgress.close()
	s.ioProgress = nil
	s.ioBusy, s.ioCancel = false, nil
}

func (a *App) viewTransferProgress(c *ui.Context, pal *palette, s *session) {
	if !s.ioBusy || s.ioProgress == nil {
		return
	}
	ui.Row(c.Key("transfer-progress")).Shrink(0).Padding(6, 12).Gap(8).AlignItems(ui.Center).Background(pal.chip).Children(func() {
		count := a.tr("%d processed · %d skipped", s.ioDone+s.ioSkipped, s.ioSkipped)
		if s.ioTotal > 0 {
			count = a.tr("%d / %d processed · %d skipped", s.ioDone+s.ioSkipped, s.ioTotal, s.ioSkipped)
		}
		ui.Text(c, a.tr(s.ioLabel)+" · "+count+" · "+s.ioElapsed.Truncate(time.Second).String()).
			FontSize(12).TextColor(pal.text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		textButton(c, "transfer-cancel", a.tr("Cancel transfer"), false, false, pal).OnClick(func() {
			if s.ioCancel != nil {
				s.ioCancel()
			}
		})
	})
}
