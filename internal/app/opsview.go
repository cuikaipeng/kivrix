package app

// Views for the server tools in the working card.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

type workTool struct {
	id, label, tip string
}

var workTools = []workTool{
	{"browser", "Browser", "Browse this database's keys"},
	{"status", "Status", "Server information and statistics"},
	{"cli", "CLI", "Run Redis commands"},
	{"slowlog", "Slow log", "The server's slow command log"},
	{"monitor", "Monitor", "Stream every command the server runs"},
	{"pubsub", "Pub/Sub", "Subscribe to channels and patterns"},
	{"cmdlog", "Cmd log", "Commands this app ran"},
}

// viewWorkTabs is the page strip at the top of the working card.
func (a *App) viewWorkTabs(c *ui.Context, pal *palette, s *session) {
	ui.ScrollHorizontal(c.Key("work-tabs")).Height(38).Shrink(0).Children(func() {
		ui.Row(c).Height(34).Padding(0, 10).Gap(2).AlignItems(ui.Center).Children(func() {
			for _, t := range workTools {
				active := s.tool == t.id
				row := ui.Row(c.Key("work-"+t.id)).Height(24).Padding(0, 7).Radius(6).AlignItems(ui.Center).FocusRing(false)
				if active {
					row.Background(pal.chip)
				} else if row.Hovered() {
					row.Background(pal.rowHover)
				}
				color := pal.muted
				if active {
					color = pal.text
				}
				row.Children(func() {
					ui.Text(c, a.tr(t.label)).FontSize(12.5).TextColor(color)
					if t.id == "monitor" && s.monitorRunning {
						ui.Box(c).Size(5, 5).Radius(3).Background(pal.success).Margin(4, 0, 0, 0)
					}
					if t.id == "pubsub" && s.pubRunning {
						ui.Box(c).Size(5, 5).Radius(3).Background(pal.success).Margin(4, 0, 0, 0)
					}
				})
				if row.Clicked() {
					s.setTool(t.id)
				}
				row.Tooltip(a.tr(t.tip))
			}
		})
	})
}

// viewTool builds the active non-browser page.
func (a *App) viewTool(c *ui.Context, pal *palette, s *session) {
	switch s.tool {
	case "status":
		a.viewStatus(c, pal, s)
	case "cli":
		a.viewCLI(c, pal, s)
	case "slowlog":
		a.viewSlowLog(c, pal, s)
	case "monitor":
		a.viewMonitor(c, pal, s)
	case "pubsub":
		a.viewPubSub(c, pal, s)
	case "cmdlog":
		a.viewCmdLog(c, pal, s)
	default:
		a.viewValueCard(c, pal, s)
	}
}

// opsHeader is the title row of a tool page.
func (a *App) opsHeader(c *ui.Context, pal *palette, title, detail string, actions func()) ui.Element {
	return ui.Row(c).Height(44).Shrink(0).Padding(0, 14).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, a.tr(title)).FontSize(14).FontWeight(600).TextColor(pal.text).SingleLine()
			if detail != "" {
				ui.Text(c, a.tr(detail)).FontSize(11).TextColor(pal.faint).SingleLine().Ellipsis("…").Tooltip(detail)
			}
		})
		if actions != nil {
			actions()
		}
	})
}

func (a *App) toolError(c *ui.Context, pal *palette, title, detail string, retry func()) {
	emptyState(c, pal, iconAlert, title, detail, a.tr("Retry"), retry)
}

type statusMetric struct {
	label, value string
}

func (a *App) viewStatus(c *ui.Context, pal *palette, s *session) {
	detail := a.tr("Server information")
	if s.store.IsCluster() {
		detail = a.tr("One cluster node")
	}
	if !s.statusUpdated.IsZero() {
		detail += " · " + a.tr("Updated") + " " + s.statusUpdated.Format("15:04:05")
	} else if s.statusLoading {
		detail = "Loading…"
	}
	a.opsHeader(c, pal, "Server status", detail, func() {
		if textButton(c, "status-clients", a.tr("Clients"), false, false, pal).Clicked() {
			s.openClients()
		}
		if s.statusRaw != "" {
			iconButton(c, "status-copy", iconCopy, a.tr("Copy INFO"), pal, func() {
				c.Services().WriteClipboard(s.statusRaw)
				s.setStatus("Copied server INFO")
			})
		}
		iconButton(c, "status-refresh", iconRefresh, a.tr("Refresh status"), pal, s.loadStatus)
	}).Height(52).Padding(0, 20)
	switch {
	case s.infoErr != "":
		a.toolError(c, pal, a.tr("Could not read server status"), s.infoErr, s.loadStatus)
	case s.statusInfo == nil && s.statusLoading:
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Spinner(c).Label(a.tr("Reading INFO…"))
		})
	case s.statusInfo == nil:
		emptyState(c, pal, iconServer, a.tr("No status yet"), a.tr("Read the server's INFO sections."), a.tr("Load status"), s.loadStatus)
	default:
		ui.Scroll(c.Key("status-scroll")).Grow(1).Padding(12, 20, 20).Children(func() {
			ui.Column(c).Gap(20).Children(func() {
				a.statusActivity(c, pal, s)
				ui.Row(c).Wrap().Gap(12).AlignItems(ui.Stretch).Children(func() {
					a.statusSection(c, pal, iconServer, a.tr("Server"), []statusMetric{
						{"Version", infoValue(s.statusInfo, "server", "redis_version")},
						{"Mode", infoValue(s.statusInfo, "server", "redis_mode")},
						{"Uptime", formatUptime(infoValue(s.statusInfo, "server", "uptime_in_seconds"))},
						{"Port", infoValue(s.statusInfo, "server", "tcp_port")},
					})
					a.statusSection(c, pal, iconDatabase, a.tr("Memory"), []statusMetric{
						{"Used", infoValue(s.statusInfo, "memory", "used_memory_human")},
						{"Peak", infoValue(s.statusInfo, "memory", "used_memory_peak_human")},
						{"Max", unlimited(infoValue(s.statusInfo, "memory", "maxmemory_human"))},
						{"Fragmentation", infoValue(s.statusInfo, "memory", "mem_fragmentation_ratio")},
					})
					a.statusSection(c, pal, iconUsers, a.tr("Clients & commands"), []statusMetric{
						{"Connected", infoValue(s.statusInfo, "clients", "connected_clients")},
						{"Blocked", infoValue(s.statusInfo, "clients", "blocked_clients")},
						{"Ops/sec", infoValue(s.statusInfo, "stats", "instantaneous_ops_per_sec")},
						{"Processed", a.statusCount(infoValue(s.statusInfo, "stats", "total_commands_processed"))},
						{"Hits", a.statusCount(infoValue(s.statusInfo, "stats", "keyspace_hits"))},
						{"Misses", a.statusCount(infoValue(s.statusInfo, "stats", "keyspace_misses"))},
					})
					a.statusSection(c, pal, iconSave, a.tr("CPU & persistence"), []statusMetric{
						{"CPU sys", infoValue(s.statusInfo, "cpu", "used_cpu_sys")},
						{"CPU user", infoValue(s.statusInfo, "cpu", "used_cpu_user")},
						{"Loading", infoValue(s.statusInfo, "persistence", "loading")},
						{"AOF", infoValue(s.statusInfo, "persistence", "aof_enabled")},
					})
					a.statusSection(c, pal, iconServer, a.tr("Replication"), []statusMetric{
						{"Role", infoValue(s.statusInfo, "replication", "role")},
						{"Slaves", infoValue(s.statusInfo, "replication", "connected_slaves")},
						{"OS", infoValue(s.statusInfo, "server", "os")},
						{"Arch", infoValue(s.statusInfo, "server", "arch_bits")},
					})
					a.statusSection(c, pal, iconRadio, a.tr("Network"), []statusMetric{
						{"In KB/s", infoValue(s.statusInfo, "stats", "instantaneous_input_kbps")},
						{"Out KB/s", infoValue(s.statusInfo, "stats", "instantaneous_output_kbps")},
						{"Total in", formatBytes(parseInt64(infoValue(s.statusInfo, "stats", "total_net_input_bytes")))},
						{"Total out", formatBytes(parseInt64(infoValue(s.statusInfo, "stats", "total_net_output_bytes")))},
					})
				})
				a.statusKeyspace(c, pal, s.statusInfo["keyspace"])
			})
		})
	}
	if s.clientsOpen {
		a.viewClients(c, pal, s)
	}
}

func (a *App) viewClients(c *ui.Context, pal *palette, s *session) {
	ui.Modal(c.Key("clients-modal"), &s.clientsOpen, func() {
		ui.Column(c).Width(700).Padding(4).Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Clients")).FontSize(15).FontWeight(650).TextColor(pal.text)
				ui.Textf(c, "%d", len(s.clients)).FontSize(11).TextColor(pal.faint)
				ui.Spacer(c)
				iconButton(c, "clients-refresh", iconRefresh, a.tr("Refresh clients"), pal, s.loadClients)
				iconButton(c, "clients-close", iconClose, a.tr("Close"), pal, func() { s.clientsOpen = false })
			})
			switch {
			case s.clientsLoading:
				ui.Row(c).Height(120).Center().Children(func() {
					ui.Spinner(c).Label(a.tr("Reading CLIENT LIST…"))
				})
			case s.clientsErr != "":
				ui.Text(c, s.clientsErr).FontSize(11.5).TextColor(pal.danger)
			case len(s.clients) == 0:
				ui.Text(c, a.tr("No clients reported")).FontSize(11.5).TextColor(pal.faint).Padding(8, 0)
			default:
				cols := []ui.TableColumn{
					{Title: a.tr("Address"), Width: 175},
					{Title: a.tr("Name"), Width: 110},
					{Title: a.tr("DB"), Width: 45, Align: ui.End},
					{Title: a.tr("Idle"), Width: 60, Align: ui.End},
					{Title: a.tr("Command")},
				}
				ui.Table(c, &s.clientsRows, cols, len(s.clients), func(row, col int) {
					cl := s.clients[row]
					text := []string{cl.addr, cl.name, cl.db, cl.idle, cl.cmd}[col]
					ui.Text(c, text).Font("monospace").FontSize(11).TextColor(pal.text).SingleLine().Tooltip(text)
				}).Height(320)
			}
		})
	})
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func (a *App) statusActivity(c *ui.Context, pal *palette, s *session) {
	label := a.tr("Sample")
	if s.statusSampling {
		label = a.tr("Stop sampling")
	}
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Height(30).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, a.tr("Activity")).FontSize(13).FontWeight(600).TextColor(pal.text)
			if s.statusSampling {
				ui.Box(c).Size(5, 5).Radius(3).Background(pal.success)
			}
			if len(s.statusHistory) > 0 {
				ui.Text(c, a.tr("%d samples", len(s.statusHistory))).FontSize(11).TextColor(pal.faint)
			}
			ui.Spacer(c)
			if textButton(c, "status-sample", label, s.statusSampling, false, pal).Clicked() {
				s.toggleStatusSampling()
			}
		})
		if len(s.statusHistory) == 0 {
			ui.Text(c, a.tr("Refresh or enable sampling to collect activity history.")).
				FontSize(12).TextColor(pal.muted).Padding(12, 16).Background(pal.chip).Radius(12)
			return
		}
		values := func(pick func(statusSample) float64) []float64 {
			out := make([]float64, len(s.statusHistory))
			for i, sample := range s.statusHistory {
				out[i] = pick(sample)
			}
			return out
		}
		last := s.statusHistory[len(s.statusHistory)-1]
		// Pairs wrap together, keeping four cards on wide panes and two on narrow ones.
		ui.Row(c).Wrap().Gap(12).Children(func() {
			ui.Row(c).Basis(310).Grow(1).MinWidth(0).Gap(12).Children(func() {
				a.miniChart(c, pal, a.tr("Clients"), a.statusCount(strconv.FormatInt(int64(last.clients), 10)),
					values(func(x statusSample) float64 { return x.clients }))
				a.miniChart(c, pal, a.tr("Memory"), formatBytes(int64(last.memory)),
					values(func(x statusSample) float64 { return x.memory }))
			})
			ui.Row(c).Basis(310).Grow(1).MinWidth(0).Gap(12).Children(func() {
				a.miniChart(c, pal, a.tr("Net in"), fmt.Sprintf("%.1f KB/s", last.input),
					values(func(x statusSample) float64 { return x.input }))
				a.miniChart(c, pal, a.tr("Net out"), fmt.Sprintf("%.1f KB/s", last.output),
					values(func(x statusSample) float64 { return x.output }))
			})
		})
	})
}

func (a *App) miniChart(c *ui.Context, pal *palette, title, current string, values []float64) {
	peak := 0.0
	for i, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			values[i] = 0
		} else {
			peak = max(peak, v)
		}
	}
	ui.Column(c.Key(title)).Grow(1).Basis(0).MinWidth(0).Padding(14, 16, 10).Radius(12).
		Background(pal.accent.Alpha(0.055)).Gap(6).Children(func() {
		ui.Text(c, title).FontSize(12).TextColor(pal.muted).SingleLine()
		ui.Text(c, current).FontSize(22).FontWeight(650).TextColor(pal.text).
			SingleLine().Ellipsis("…").Tooltip(current)
		ui.Box(c).Height(32).Margin(4, 0, 0).Draw(func(p *ui.Painter, r ui.Rect) {
			left, right, bottom := r.X+3, r.X+r.W-3, r.Y+r.H-3
			p.Line(left, bottom, right, bottom, 1, pal.divider)
			if len(values) == 0 || r.W <= 6 {
				return
			}
			// Keep a fixed sample window: one reading is a dot, not a fabricated trend.
			step := (right - left) / float32(max(maxStatusHistory-1, len(values)-1))
			var line, area ui.Path
			var x, y float32
			for i, v := range values {
				x = right - float32(len(values)-1-i)*step
				y = bottom
				if peak > 0 {
					y -= float32(v/peak) * (r.H - 8)
				}
				if i == 0 {
					line.MoveTo(x, y)
					area.MoveTo(x, bottom).LineTo(x, y)
				} else {
					line.LineTo(x, y)
					area.LineTo(x, y)
				}
			}
			if len(values) > 1 {
				area.LineTo(right, bottom).Close()
				p.FillPath(&area, pal.accent.Alpha(0.08))
				p.StrokePath(&line, 1.8, pal.accent)
			}
			p.Fill(ui.Rect{X: x - 2.5, Y: y - 2.5, W: 5, H: 5}, pal.accent, 2.5)
		})
	})
}

func (a *App) statusSection(c *ui.Context, pal *palette, glyph *ui.SVG, title string, metrics []statusMetric) {
	ui.Column(c.Key(title)).Basis(300).Grow(1).MinWidth(0).Padding(14, 16).Radius(12).
		Border(1, pal.cardBorder).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, glyph).Size(15, 15).TextColor(pal.muted)
			ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(pal.text)
		})
		ui.Column(c).Children(func() {
			for _, m := range metrics {
				value := m.value
				if value == "" {
					value = "—"
				}
				ui.Row(c).MinHeight(28).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.tr(m.label)).FontSize(12).TextColor(pal.muted).Grow(1).MinWidth(0).
						SingleLine().Ellipsis("…").Tooltip(a.tr(m.label))
					ui.Text(c, value).FontSize(12.5).FontWeight(500).TextColor(pal.text).
						MaxWidthPercent(65).TextAlign(ui.End).SingleLine().Ellipsis("…").Tooltip(value)
				})
			}
		})
	})
}

func (a *App) statusCount(value string) string {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}
	return i18n.FormatNumber(a.locale(), n, i18n.NumberOptions{})
}

func (a *App) statusKeyspace(c *ui.Context, pal *palette, keyspace map[string]string) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconDatabase).Size(15, 15).TextColor(pal.muted)
			ui.Text(c, a.tr("Keyspace")).FontSize(13).FontWeight(600).TextColor(pal.text)
		})
		if len(keyspace) == 0 {
			ui.Text(c, a.tr("No keys in this server.")).FontSize(12).TextColor(pal.muted).Padding(12, 0)
			return
		}
		dbs := make([]string, 0, len(keyspace))
		for db := range keyspace {
			dbs = append(dbs, db)
		}
		sort.Slice(dbs, func(i, j int) bool {
			left, le := strconv.Atoi(strings.TrimPrefix(dbs[i], "db"))
			right, re := strconv.Atoi(strings.TrimPrefix(dbs[j], "db"))
			if le == nil && re == nil && left != right {
				return left < right
			}
			return dbs[i] < dbs[j]
		})
		ui.Column(c).Radius(12).Border(1, pal.cardBorder).Clip().Children(func() {
			ui.Grid(c).Columns(3).Gap(12).Padding(10, 14).Background(pal.chip).Children(func() {
				ui.Text(c, a.tr("Database")).FontSize(11.5).FontWeight(600).TextColor(pal.muted)
				ui.Text(c, a.tr("Key count")).FontSize(11.5).FontWeight(600).TextColor(pal.muted).TextAlign(ui.End)
				ui.Text(c, a.tr("With expiration")).FontSize(11.5).FontWeight(600).TextColor(pal.muted).TextAlign(ui.End)
			})
			for i, db := range dbs {
				if i > 0 {
					ui.Box(c).Height(1).Background(pal.divider).Margin(0, 14)
				}
				keys, expires := parseKeyspace(keyspace[db])
				ui.Grid(c.Key(db)).Columns(3).Gap(12).Padding(10, 14).AlignItems(ui.Center).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(5, 5).Radius(3).Background(pal.accent.Alpha(0.6))
						ui.Text(c, db).FontSize(12.5).FontWeight(500).TextColor(pal.text)
					})
					ui.Text(c, a.statusCount(strconv.FormatInt(keys, 10))).FontSize(12.5).TextColor(pal.text).TextAlign(ui.End)
					ui.Text(c, a.statusCount(strconv.FormatInt(expires, 10))).FontSize(12.5).TextColor(pal.muted).TextAlign(ui.End)
				})
			}
		})
	})
}

func parseKeyspace(v string) (keys, expires int64) {
	for _, field := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(val, 10, 64)
		switch k {
		case "keys":
			keys = n
		case "expires":
			expires = n
		}
	}
	return keys, expires
}

func unlimited(v string) string {
	if v == "0" || v == "0B" {
		return "unlimited"
	}
	return v
}

func formatUptime(v string) string {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return v
	}
	d := time.Duration(n) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func (a *App) viewCLI(c *ui.Context, pal *palette, s *session) {
	a.opsHeader(c, pal, "CLI", fmt.Sprintf("%s · ↑/↓ %s", s.cfg.dbLabel(s.db), a.tr("Command history")), func() {
		if len(s.cliEntries) > 0 {
			iconButton(c, "cli-clear", iconTrash, a.tr("Clear output"), pal, s.clearCLI)
		}
	})
	if len(s.cliEntries) == 0 {
		emptyState(c, pal, iconTerminal, a.tr("Run a command"), a.tr("Type a Redis command below; quote values with spaces."), "", nil)
	} else {
		s.cliRows.FollowEnd = true
		ui.List(c, &s.cliRows, len(s.cliEntries)).Grow(1).TrackScroll(&s.cliScroll).Padding(10, 14).Gap(8).Rows(func(row ui.ListRow) {
			c, e := row.Context, s.cliEntries[row.Index]
			ui.Column(c).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "›").FontSize(12).TextColor(pal.accent)
					ui.Text(c, e.command).Font(a.prefs.CLIFont).FontSize(float32(a.prefs.CLISize)).TextColor(pal.text).Selectable()
				})
				color := pal.muted
				if e.err {
					color = pal.danger
				}
				ui.Text(c, e.result).Font(a.prefs.CLIFont).FontSize(float32(a.prefs.CLISize)).TextColor(color).Selectable()
			})
		})
	}
	ui.Row(c).Height(44).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "›").FontSize(13).TextColor(pal.accent)
		s.cliHandle.OnShortcut(c, 0, ui.KeyUp, func() { s.browseCLI(-1) })
		s.cliHandle.OnShortcut(c, 0, ui.KeyDown, func() { s.browseCLI(1) })
		input := ui.TextInput(c.Key("cli-input"), &s.cliInput).Bind(&s.cliHandle).Label(a.tr("Command input")).Placeholder("SET key value").Grow(1).Font(a.prefs.CLIFont).FontSize(float32(a.prefs.CLISize)).Disabled(s.cliBusy || s.dbSwitching)
		if input.Submitted() {
			s.runCLI()
		}
		label := a.tr("Run")
		if s.cliBusy {
			label = a.tr("Running…")
		}
		if textButton(c, "cli-run", label, true, false, pal).Disabled(s.cliBusy).Clicked() {
			s.runCLI()
		}
	})
}

func (a *App) viewSlowLog(c *ui.Context, pal *palette, s *session) {
	filtered := s.filteredSlow()
	detail := s.slowLogSummary()
	if s.slowFilter != "" {
		detail = a.tr("%d of %d entries", len(filtered), len(s.slowEntries))
	}
	if s.store.IsCluster() {
		detail += " · " + a.tr("one node")
	}
	if s.slowLoading {
		detail = "Loading…"
	}
	a.opsHeader(c, pal, "Slow log", detail, func() {
		iconButton(c, "slow-refresh", iconRefresh, a.tr("Refresh slow log"), pal, s.loadSlowLog)
		if s.slowTotal > 0 {
			iconButton(c, "slow-reset", iconTrash, a.tr("Reset slow log"), pal, func() {
				a.askSlowReset(s)
			}).Disabled(s.cfg.ReadOnly)
		}
	})
	ui.Row(c).Height(36).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		a.searchField(c, pal, &s.slowFilter, "Filter entries").Grow(1)
		if ui.Select(c, &s.slowLimitChoice, []string{"64", "128", "256", "512"}).Width(82).Changed() {
			s.loadSlowLog()
		}
	})
	switch {
	case s.slowErr != "":
		a.toolError(c, pal, a.tr("Could not read the slow log"), s.slowErr, s.loadSlowLog)
	case s.slowLoading && len(s.slowEntries) == 0:
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Spinner(c).Label(a.tr("Reading slow log…"))
		})
	case len(s.slowEntries) == 0:
		emptyState(c, pal, iconClock, a.tr("No slow commands"), a.tr("Commands over the server's slowlog-log-slower-than threshold show here."), "", nil)
	case len(filtered) == 0:
		emptyState(c, pal, iconSearch, a.tr("No matching slow entries"), "", "", nil)
	default:
		cols := []ui.TableColumn{
			{Title: a.tr("Time"), Width: 72},
			{Title: a.tr("Duration"), Width: 78, Align: ui.End},
			{Title: a.tr("Client"), Width: 128},
			{Title: a.tr("Command")},
		}
		ui.Table(c, &s.slowRows, cols, len(filtered), func(row, col int) {
			e := filtered[row]
			switch col {
			case 0:
				ui.Text(c, e.at.Format("15:04:05")).FontSize(11).TextColor(pal.muted).SingleLine()
			case 1:
				ui.Text(c, formatDuration(e.duration)).FontSize(11).TextColor(pal.warning).SingleLine()
			case 2:
				ui.Text(c, e.client).FontSize(11).TextColor(pal.faint).SingleLine().Ellipsis("…").Tooltip(e.client)
			case 3:
				ui.Text(c, e.command).Font("monospace").FontSize(11).TextColor(pal.text).
					SingleLine().Tooltip(e.command + clientSuffix(e.client))
			}
		}).Grow(1)
	}
}

func clientSuffix(client string) string {
	if client == "" {
		return ""
	}
	return "\nfrom " + client
}

func formatDuration(d time.Duration) string {
	if d >= time.Millisecond {
		return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.0f µs", float64(d)/float64(time.Microsecond))
}

func (a *App) viewMonitor(c *ui.Context, pal *palette, s *session) {
	lines := s.filteredMonitor()
	detail := a.tr("Live command stream")
	if s.store.IsCluster() {
		detail = a.tr("Live stream from one cluster node")
	}
	if !s.monitorRunning {
		detail = a.tr("Stopped")
	} else if n := len(s.monitorLines); n > 0 {
		detail = a.tr("Live · %d lines", n)
	}
	if s.monitorFilter != "" {
		detail = a.tr("%d of %d lines", len(lines), len(s.monitorLines))
	}
	a.opsHeader(c, pal, "Monitor", detail, func() {
		if len(lines) > 0 {
			iconButton(c, "monitor-copy", iconCopy, a.tr("Copy log"), pal, func() {
				a.copyOpsText(c, s, strings.Join(lines, "\n"), "monitor log")
			})
			iconButton(c, "monitor-save", iconSave, a.tr("Save log"), pal, func() {
				a.saveOpsText(s, "Save monitor log", "monitor.log", strings.Join(lines, "\n"))
			})
		}
		followLabel := "Follow live"
		if !s.monitorFollow {
			followLabel = "Follow paused"
		}
		iconButton(c, "monitor-follow", iconChevronDown, a.tr(followLabel), pal, func() {
			s.monitorFollow = !s.monitorFollow
			if s.monitorFollow {
				s.monitorScroll.ScrollTo(0, math.MaxFloat32, ui.Smooth)
			}
		})
		if s.monitorRunning {
			iconButton(c, "monitor-stop", iconStop, a.tr("Stop monitoring"), pal, s.stopMonitor)
		} else {
			iconButton(c, "monitor-start", iconPlay, a.tr("Start monitoring"), pal, s.startMonitor)
		}
		if len(s.monitorLines) > 0 {
			iconButton(c, "monitor-clear", iconTrash, a.tr("Clear monitor"), pal, s.clearMonitor)
		}
	})
	ui.Row(c).Height(36).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		a.searchField(c, pal, &s.monitorFilter, "Filter lines").Grow(1)
	})
	if s.monitorErr != "" {
		ui.Row(c).Padding(8, 14).Children(func() {
			ui.Text(c, s.monitorErr).FontSize(11.5).TextColor(pal.danger).SingleLine().Tooltip(s.monitorErr)
		})
	}
	switch {
	case len(s.monitorLines) == 0 && s.monitorRunning:
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Spinner(c).Label(a.tr("Waiting for commands…"))
		})
	case len(s.monitorLines) == 0:
		emptyState(c, pal, iconActivity, a.tr("Monitor stopped"), a.tr("Start it to stream every command the server runs."), a.tr("Start monitor"), s.startMonitor)
	case len(lines) == 0:
		emptyState(c, pal, iconSearch, a.tr("No matching lines"), "", "", nil)
	default:
		s.monitorRows.FollowEnd = s.monitorFollow
		ui.List(c, &s.monitorRows, len(lines)).Grow(1).TrackScroll(&s.monitorScroll).Padding(8, 12).Rows(func(row ui.ListRow) {
			// Render only visible rows; never shape the entire multi-MiB log.
			line := lines[row.Index]
			ui.Text(row.Context, limitedText(line, 512)).Font("monospace").FontSize(11).
				TextColor(pal.text).SingleLine().Ellipsis("…").Height(19).Selectable().Tooltip(line)
		})
	}
}

func (a *App) viewPubSub(c *ui.Context, pal *palette, s *session) {
	messages := s.filteredPub()
	detail := a.tr("Not subscribed")
	if s.pubRunning {
		detail = a.tr("Subscribed to") + " " + strings.Join(s.pubChannels, ", ")
	}
	if s.pubFilter != "" {
		detail = a.tr("%d of %d messages", len(messages), len(s.pubMessages))
	} else if s.pubRunning {
		detail += " · " + a.tr("%d messages", len(s.pubMessages))
	}
	a.opsHeader(c, pal, "Pub/Sub", detail, func() {
		if len(messages) > 0 {
			iconButton(c, "pub-copy", iconCopy, a.tr("Copy messages"), pal, func() {
				a.copyOpsText(c, s, pubLog(messages), "pub/sub messages")
			})
			iconButton(c, "pub-save", iconSave, a.tr("Save messages"), pal, func() {
				a.saveOpsText(s, "Save pub/sub messages", "pubsub.log", pubLog(messages))
			})
		}
		followLabel := "Follow live"
		if !s.pubFollow {
			followLabel = "Follow paused"
		}
		iconButton(c, "pub-follow", iconChevronDown, a.tr(followLabel), pal, func() {
			s.pubFollow = !s.pubFollow
			if s.pubFollow {
				s.pubScroll.ScrollTo(0, math.MaxFloat32, ui.Smooth)
			}
		})
		if len(s.pubMessages) > 0 {
			iconButton(c, "pub-clear", iconTrash, a.tr("Clear messages"), pal, s.clearPub)
		}
	})
	ui.Row(c).Height(42).Shrink(0).Padding(0, 12).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c, &s.pubChannel).Label(a.tr("channel")).Placeholder(a.tr("channel")).Width(150)
		input := ui.TextInput(c, &s.pubPayload).Label(a.tr("message")).Placeholder(a.tr("message")).Grow(1)
		label := a.tr("Publish")
		if s.pubPublishBusy {
			label = a.tr("Publishing…")
		}
		b := textButton(c, "pub-publish", label, true, false, pal).Disabled(s.cfg.ReadOnly || s.pubPublishBusy || strings.TrimSpace(s.pubChannel) == "")
		if b.Clicked() || (input.Submitted() && !s.cfg.ReadOnly && strings.TrimSpace(s.pubChannel) != "") {
			s.publish()
		}
	})
	ui.Row(c).Height(42).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Checkbox(c, &s.pubPatterns, a.tr("Patterns")).Disabled(s.pubRunning)
		if s.pubRunning {
			ui.Text(c, strings.Join(s.pubChannels, ", ")).FontSize(11.5).TextColor(pal.text).
				Grow(1).SingleLine().Ellipsis("…").Tooltip(strings.Join(s.pubChannels, ", "))
		} else {
			input := ui.TextInput(c, &s.pubInput).Label(a.tr("Enter a channel or pattern.")).Placeholder(a.tr("news, sports")).Grow(1)
			if input.Submitted() {
				s.startPubSub()
			}
		}
		if s.pubRunning {
			if textButton(c, "pub-stop", a.tr("Stop"), false, false, pal).Clicked() {
				s.stopPubSub()
			}
		} else if textButton(c, "pub-start", a.tr("Subscribe"), true, false, pal).Clicked() {
			s.startPubSub()
		}
	})
	ui.Row(c).Height(34).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		a.searchField(c, pal, &s.pubFilter, "Filter messages").Grow(1)
	})
	if s.pubErr != "" {
		ui.Row(c).Padding(6, 14).Children(func() {
			ui.Text(c, a.tr(s.pubErr)).FontSize(11.5).TextColor(pal.danger).SingleLine().Tooltip(s.pubErr)
		})
	}
	switch {
	case len(s.pubMessages) == 0 && s.pubRunning:
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Spinner(c).Label(a.tr("Waiting for messages…"))
		})
	case len(s.pubMessages) == 0:
		emptyState(c, pal, iconRadio, a.tr("No messages yet"), a.tr("Subscribe to a channel or pattern to watch messages arrive."), "", nil)
	case len(messages) == 0:
		emptyState(c, pal, iconSearch, a.tr("No matching messages"), "", "", nil)
	default:
		s.pubRows.FollowEnd = s.pubFollow
		ui.List(c, &s.pubRows, len(messages)).Grow(1).TrackScroll(&s.pubScroll).Padding(4, 12).Rows(func(row ui.ListRow) {
			c, m := row.Context, messages[row.Index]
			ui.Column(c).Height(52).Padding(5, 4).Gap(2).Children(func() {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Text(c, m.at.Format("15:04:05")).FontSize(11).TextColor(pal.faint)
					channel := m.channel
					if m.pattern != "" {
						channel = m.pattern + " → " + channel
					}
					ui.Text(c, limitedText(channel, 512)).FontSize(11).FontWeight(600).TextColor(pal.accent).SingleLine().Ellipsis("…")
				})
				ui.Text(c, limitedText(m.payload, 512)).Font("monospace").FontSize(11.5).TextColor(pal.text).SingleLine().Ellipsis("…").Selectable().Tooltip(m.payload)
			})
		})
	}
}

func pubLog(messages []pubMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		channel := m.channel
		if m.pattern != "" {
			channel = m.pattern + " → " + m.channel
		}
		fmt.Fprintf(&sb, "%s %s %s\n", m.at.Format("15:04:05"), channel, m.payload)
	}
	return sb.String()
}

func (a *App) viewCmdLog(c *ui.Context, pal *palette, s *session) {
	filtered := s.filteredCommands()
	detail := a.tr("%d commands", len(s.cmdLog))
	if s.cmdFilter != "" {
		detail = a.tr("%d of %d commands", len(filtered), len(s.cmdLog))
	}
	a.opsHeader(c, pal, "Command log", detail, func() {
		if len(filtered) > 0 {
			iconButton(c, "cmdlog-copy", iconCopy, a.tr("Copy command log"), pal, func() {
				a.copyOpsText(c, s, commandLogText(filtered), "command log")
			})
			iconButton(c, "cmdlog-save", iconSave, a.tr("Save command log"), pal, func() {
				a.saveOpsText(s, "Save command log", "commands.log", commandLogText(filtered))
			})
		}
		if len(s.cmdLog) > 0 {
			iconButton(c, "cmdlog-clear", iconTrash, a.tr("Clear command log"), pal, s.clearCommandLog)
		}
	})
	ui.Row(c).Height(36).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		a.searchField(c, pal, &s.cmdFilter, "Filter commands").Grow(1)
	})
	switch {
	case len(s.cmdLog) == 0:
		emptyState(c, pal, iconList, a.tr("No commands yet"), a.tr("Commands this app runs on the connection appear here."), "", nil)
	case len(filtered) == 0:
		emptyState(c, pal, iconSearch, a.tr("No matching commands"), "", "", nil)
	default:
		s.cmdRows.FollowEnd = true
		ui.List(c, &s.cmdRows, len(filtered)).Grow(1).TrackScroll(&s.cmdScroll).Padding(6, 12).Rows(func(row ui.ListRow) {
			c, e := row.Context, filtered[row.Index]
			ui.Column(c).Gap(3).Children(func() {
				ui.Row(c).Height(21).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, e.At.Format("15:04:05")).FontSize(11).TextColor(pal.faint).Width(52)
					command := e.Command()
					ui.Text(c, limitedText(command, 512)).Font("monospace").FontSize(11).TextColor(pal.text).
						Grow(1).SingleLine().Ellipsis("…").Tooltip(limitedText(command, maxLiveText))
					ui.Text(c, formatDuration(e.Dur)).FontSize(11).TextColor(pal.faint)
				})
				if e.Err != "" {
					ui.Text(c, e.Err).FontSize(11).TextColor(pal.danger).SingleLine().Ellipsis("…").Tooltip(e.Err)
				}
			})
		})
	}
}

func commandLogText(entries []CommandEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s %s %s", e.At.Format("15:04:05"), e.Command(), formatDuration(e.Dur))
		if e.Err != "" {
			fmt.Fprintf(&sb, " error=%s", e.Err)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (a *App) viewStreamGroups(c *ui.Context, pal *palette, s *session) {
	ui.Modal(c.Key("stream-groups"), &s.groupsOpen, func() {
		ui.Column(c).Width(680).Padding(4).Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Consumer groups")).FontSize(15).FontWeight(650).TextColor(pal.text)
				ui.Textf(c, "%d", len(s.groups)).FontSize(11).TextColor(pal.faint)
				ui.Spacer(c)
				iconButton(c, "groups-refresh", iconRefresh, a.tr("Refresh groups"), pal, s.loadStreamGroups)
				iconButton(c, "groups-close", iconClose, a.tr("Close"), pal, func() { s.groupsOpen = false })
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &s.groupName).Placeholder(a.tr("group name")).Width(180)
				ui.TextInput(c, &s.groupStart).Placeholder(a.tr("$ or 0")).Width(90)
				label := a.tr("Create group")
				if s.groupsBusy {
					label = a.tr("Working…")
				}
				if textButton(c, "group-create", label, true, false, pal).Disabled(s.cfg.ReadOnly || s.groupsBusy || strings.TrimSpace(s.groupName) == "").Clicked() {
					s.createStreamGroup()
				}
			})
			if s.groupsErr != "" {
				ui.Text(c, s.groupsErr).FontSize(11).TextColor(pal.danger).SingleLine().Ellipsis("…").Tooltip(s.groupsErr)
			}
			switch {
			case s.groupsLoading && len(s.groups) == 0:
				ui.Row(c).Height(100).Center().Children(func() {
					ui.Spinner(c).Label(a.tr("Reading XINFO GROUPS…"))
				})
			case len(s.groups) == 0:
				ui.Text(c, a.tr("No consumer groups yet.")).FontSize(11.5).TextColor(pal.faint).Padding(8, 0)
			default:
				ui.Scroll(c).MaxHeight(150).Children(func() {
					ui.Column(c).Gap(3).Children(func() {
						for _, g := range s.groups {
							selected := s.groupSelected == g.Name
							row := ui.Row(c.Key("group-"+g.Name)).Height(34).Padding(5, 8).Gap(8).Radius(6).
								AlignItems(ui.Center)
							if selected {
								row.Background(pal.accent.Alpha(0.12))
							} else {
								row.Background(pal.chip)
							}
							row.Children(func() {
								ui.Text(c, g.Name).FontSize(11.5).FontWeight(600).TextColor(pal.text).Grow(1).
									SingleLine().Ellipsis("…").Tooltip(g.Name)
								ui.Text(c, a.tr("%d consumers", g.Consumers)).FontSize(11).TextColor(pal.faint)
								ui.Text(c, a.tr("%d pending", g.Pending)).FontSize(11).TextColor(pal.faint)
								if g.Lag >= 0 {
									ui.Text(c, a.tr("lag %d", g.Lag)).FontSize(11).TextColor(pal.faint)
								}
								ui.Text(c, g.LastDeliveredID).Font("monospace").FontSize(11).TextColor(pal.muted).Width(105)
								iconButton(c, "group-destroy-"+g.Name, iconTrash, a.tr("Destroy group"), pal, func() {
									a.askDestroyStreamGroup(s, g.Name)
								}).Disabled(s.cfg.ReadOnly)
							})
							if row.Clicked() {
								s.selectStreamGroup(g.Name)
							}
						}
					})
				})
			}
			if s.groupSelected != "" {
				ui.Box(c).Height(1).Background(pal.divider)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, s.groupSelected).FontSize(12).FontWeight(650).TextColor(pal.text).Grow(1).SingleLine().Ellipsis("…")
					ui.Text(c, a.tr("Claim to")).FontSize(11).TextColor(pal.faint)
					ui.TextInput(c, &s.groupConsumer).Width(100)
					ui.Text(c, a.tr("Min idle ms")).FontSize(11).TextColor(pal.faint)
					ui.TextInput(c, &s.groupMinIdle).Width(65)
					label := a.tr("Auto-claim")
					if s.groupAutoClaiming {
						label = a.tr("Claiming…")
					}
					if textButton(c, "group-autoclaim", label, false, false, pal).
						Disabled(s.cfg.ReadOnly || s.groupActionBusy || strings.TrimSpace(s.groupConsumer) == "").Clicked() {
						s.autoClaimPending()
					}
				})
				if s.groupDetailErr != "" {
					ui.Text(c, a.tr(s.groupDetailErr)).FontSize(11).TextColor(pal.danger).SingleLine().Ellipsis("…").Tooltip(s.groupDetailErr)
				}
				if s.groupDetailLoading {
					ui.Row(c).Height(80).Center().Children(func() {
						ui.Spinner(c).Label(a.tr("Reading group details…"))
					})
				} else {
					ui.Scroll(c).MaxHeight(190).Children(func() {
						ui.Column(c).Gap(4).Children(func() {
							ui.Textf(c, "%s · %d", a.tr("Consumers"), len(s.groupConsumers)).
								FontSize(11).FontWeight(650).TextColor(pal.muted)
							if len(s.groupConsumers) == 0 {
								ui.Text(c, a.tr("No consumers.")).FontSize(11).TextColor(pal.faint)
							}
							for _, consumer := range s.groupConsumers {
								row := ui.Row(c).Key("consumer-" + consumer.Name).Height(24).Gap(8).AlignItems(ui.Center)
								row.Children(func() {
									ui.Text(c, consumer.Name).FontSize(11).TextColor(pal.text).Grow(1).SingleLine()
									ui.Text(c, a.tr("%d pending", consumer.Pending)).FontSize(11).TextColor(pal.faint)
									ui.Text(c, formatIdle(consumer.Idle)).FontSize(11).TextColor(pal.faint)
								})
								row.ContextMenu(func(m *ui.Menu) {
									if m.Item(a.tr("Delete consumer")).Disabled(s.cfg.ReadOnly).Chosen() {
										s.deleteStreamConsumer(consumer.Name)
									}
								})
							}
							ui.Row(c).Height(20).Gap(6).AlignItems(ui.Center).Margin(4, 0, 0, 0).Children(func() {
								ui.Textf(c, "%s · %d", a.tr("Pending"), len(s.groupPending)).
									FontSize(11).FontWeight(650).TextColor(pal.muted)
								ui.Spacer(c)
								if s.groupPendingMore {
									iconButton(c, "pending-more", iconChevronDown, a.tr("Load more pending"), pal, s.loadMoreGroupPending)
								}
							})
							if len(s.groupPending) == 0 {
								ui.Text(c, a.tr("No pending entries.")).FontSize(11).TextColor(pal.faint)
							} else {
								ui.Row(c).Height(16).Gap(6).AlignItems(ui.Center).Children(func() {
									ui.Text(c, a.tr("ID")).FontSize(11).FontWeight(650).TextColor(pal.faint).Width(125)
									ui.Text(c, a.tr("Consumer")).FontSize(11).FontWeight(650).TextColor(pal.faint).Width(110)
									ui.Text(c, a.tr("Idle")).FontSize(11).FontWeight(650).TextColor(pal.faint).Width(60)
									ui.Text(c, a.tr("Retries")).FontSize(11).FontWeight(650).TextColor(pal.faint).Width(42)
									ui.Text(c, a.tr("Ack / Claim")).FontSize(11).FontWeight(650).TextColor(pal.faint)
								})
							}
							for _, p := range s.groupPending {
								ui.Row(c).Height(24).Gap(6).AlignItems(ui.Center).Children(func() {
									ui.Text(c, p.ID).Font("monospace").FontSize(11).TextColor(pal.text).Width(125).SingleLine()
									ui.Text(c, p.Consumer).FontSize(11).TextColor(pal.muted).Width(110).SingleLine().Ellipsis("…")
									ui.Text(c, formatIdle(p.Idle)).FontSize(11).TextColor(pal.faint).Width(60)
									ui.Textf(c, "%d", p.RetryCount).FontSize(11).TextColor(pal.faint).Width(42)
									iconButton(c, "pending-ack-"+p.ID, iconCheck, a.tr("Acknowledge"), pal, func() {
										s.ackPending(p.ID)
									}).Disabled(s.cfg.ReadOnly)
									iconButton(c, "pending-claim-"+p.ID, iconUsers, a.tr("Claim"), pal, func() {
										s.claimPending(p.ID)
									}).Disabled(s.cfg.ReadOnly)
								})
							}
						})
					})
				}
			} else if len(s.groups) > 0 {
				ui.Text(c, a.tr("Select a group to inspect consumers and pending entries.")).
					FontSize(11).TextColor(pal.faint)
			}
			ui.Text(c, a.tr("Consumers can be deleted; pending entries can be acknowledged, claimed or auto-claimed.")).
				FontSize(11).TextColor(pal.faint)
		})
	})
}

func formatIdle(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.0fs", d.Seconds())
	default:
		return formatDuration(d)
	}
}

func (a *App) askDestroyStreamGroup(s *session, name string) {
	if !s.writable() {
		return
	}
	if s == nil || name == "" {
		return
	}
	a.groupDestroyID = s.id
	a.groupDestroyName = name
	a.groupDestroyOpen = true
}

func (a *App) viewGroupDestroy(c *ui.Context) {
	if !a.groupDestroyOpen {
		return
	}
	s := a.sessionByID(a.groupDestroyID)
	if s == nil {
		a.groupDestroyOpen = false
		return
	}
	switch ui.AlertDialog(c, &a.groupDestroyOpen, a.tr("Destroy consumer group?"),
		s.connectionContext()+"\n\n"+a.tr("Destroy %q? Pending entries for this group will be lost.", a.groupDestroyName),
		a.tr("Cancel"), a.tr("Destroy")) {
	case 1:
		if s := a.sessionByID(a.groupDestroyID); s != nil {
			s.destroyStreamGroup(a.groupDestroyName)
		}
		a.groupDestroyName = ""
	}
}

func (a *App) viewStreamTrim(c *ui.Context, pal *palette, s *session) {
	ui.Modal(c.Key("stream-trim"), &s.trimOpen, func() {
		ui.Column(c).Width(420).Padding(4).Gap(10).Children(func() {
			ui.Text(c, a.tr("Trim stream")).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Text(c, s.connectionContext()).FontSize(12).TextColor(pal.danger)
			ui.Field(c, a.tr("Mode"), func() {
				ui.Select(c, &s.trimMode, []string{"MAXLEN", "MINID"}).Width(150)
			})
			label := a.tr("Max length")
			if s.trimMode == "MINID" {
				label = a.tr("Min ID")
			}
			ui.Field(c, label, func() {
				ui.TextInput(c, &s.trimValue).Width(240).Placeholder("1000 / 1700-0")
			})
			ui.Checkbox(c, &s.trimApprox, a.tr("Approximate (~)"))
			ui.Text(c, a.tr("Trimming removes old stream entries and cannot be undone.")).
				FontSize(11).TextColor(pal.warning).LineHeight(1.3)
			if s.trimErr != "" {
				ui.Text(c, s.trimErr).FontSize(11).TextColor(pal.danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "trim-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					s.trimOpen = false
				}
				label := a.tr("Trim")
				if s.trimBusy {
					label = a.tr("Trimming…")
				}
				if textButton(c, "trim-ok", label, true, false, pal).Disabled(s.cfg.ReadOnly || s.trimBusy).Clicked() {
					s.trimStream()
				}
			})
		})
	})
}
