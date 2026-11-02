package app

// Server tools: status, CLI, slow log, monitor, pub/sub and command log.

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	maxCLIEntries  = 100
	maxMonitorRows = 1000
	maxPubRows     = 500
	maxCommandRows = 500
	maxLiveText    = 4 << 10
)

type cliEntry struct {
	command string
	result  string
	err     bool
}

type slowEntry struct {
	at       time.Time
	duration time.Duration
	command  string
	client   string
}

type pubMessage struct {
	at      time.Time
	channel string
	pattern string
	payload string
}

// setTool switches the working card and starts or stops live feeds.
func (s *session) setTool(tool string) {
	switch tool {
	case "browser", "status", "cli", "slowlog", "monitor", "pubsub", "cmdlog":
	default:
		tool = "browser"
	}
	if s.tool == tool {
		return
	}
	if s.tool == "status" {
		s.stopStatusSampling()
	}
	s.stopMonitor()
	s.stopPubSub()
	s.tool = tool
	switch tool {
	case "status":
		s.loadStatus()
	case "slowlog":
		s.loadSlowLog()
	case "monitor":
		s.startMonitor()
	}
}

// statusSample is one point in the status activity charts.
type statusSample struct {
	clients float64
	memory  float64
	input   float64
	output  float64
}

const maxStatusHistory = 40

func statusNumber(info map[string]map[string]string, section, key string) float64 {
	n, _ := strconv.ParseFloat(infoValue(info, section, key), 64)
	return n
}

func (s *session) addStatusSample() {
	if s.statusInfo == nil {
		return
	}
	s.statusHistory = append(s.statusHistory, statusSample{
		clients: statusNumber(s.statusInfo, "clients", "connected_clients"),
		memory:  statusNumber(s.statusInfo, "memory", "used_memory"),
		input:   statusNumber(s.statusInfo, "stats", "instantaneous_input_kbps"),
		output:  statusNumber(s.statusInfo, "stats", "instantaneous_output_kbps"),
	})
	if len(s.statusHistory) > maxStatusHistory {
		s.statusHistory = s.statusHistory[len(s.statusHistory)-maxStatusHistory:]
	}
}

func (s *session) toggleStatusSampling() {
	if s.statusSampling {
		s.stopStatusSampling()
		s.setStatus("Status sampling off")
		return
	}
	if !s.store.Connected() {
		s.setStatusErr(errors.New("not connected"))
		return
	}
	s.statusSampling = true
	s.setStatus("Sampling status every %gs", s.app.prefs.refreshInterval().Seconds())
	s.loadStatus()
}

func (s *session) stopStatusSampling() {
	s.statusSampling = false
	if s.statusTimer != nil {
		s.statusTimer.Stop()
		s.statusTimer = nil
	}
}

func (s *session) scheduleStatusSample() {
	if !s.statusSampling || s.closed || s.tool != "status" {
		return
	}
	if s.statusTimer != nil {
		s.statusTimer.Stop()
	}
	s.statusTimer = time.AfterFunc(s.app.prefs.refreshInterval(), func() {
		s.app.update(func() {
			if s.statusSampling && !s.closed && s.tool == "status" {
				s.loadStatus()
			}
		})
	})
}

// loadStatus reads INFO into sections.
func (s *session) loadStatus() {
	if s.closed || s.statusLoading || !s.store.Connected() {
		return
	}
	s.statusID++
	id := s.statusID
	s.statusLoading = true
	s.infoErr = ""
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		raw, err := st.Info(ctx)
		var info map[string]map[string]string
		if len(raw) > maxEditorBytes {
			err = errValueLimit
		}
		if err == nil {
			info = parseInfo(raw)
		}
		s.app.update(func() {
			if id != s.statusID || s.closed {
				return
			}
			s.statusLoading = false
			if err != nil {
				s.infoErr = err.Error()
				if s.statusSampling {
					s.scheduleStatusSample()
				}
				return
			}
			s.statusRaw = raw
			s.statusInfo = info
			s.statusUpdated = time.Now()
			s.addStatusSample()
			if s.statusSampling {
				s.scheduleStatusSample()
			}
		})
	}()
}

// parseInfo turns "# Server\nredis_version:7.2" into sections.
func parseInfo(raw string) map[string]map[string]string {
	out := map[string]map[string]string{}
	section := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			section = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "#")))
			if out[section] == nil {
				out[section] = map[string]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || section == "" {
			continue
		}
		out[section][strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func infoValue(info map[string]map[string]string, section, key string) string {
	if info == nil {
		return ""
	}
	return info[section][key]
}

// loadSlowLog refreshes the server's slow log.
func (s *session) loadSlowLog() {
	if s.closed || s.slowLoading || !s.store.Connected() {
		return
	}
	s.slowID++
	id := s.slowID
	s.slowLoading = true
	s.slowErr = ""
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		entries, total, err := st.SlowLog(ctx, 128)
		out := make([]slowEntry, 0, len(entries))
		for _, e := range entries {
			client := limitedText(e.ClientAddr, maxLiveText)
			if e.ClientName != "" {
				client += " (" + limitedText(e.ClientName, maxLiveText) + ")"
			}
			command := ""
			if len(e.Args) > 0 {
				entry := CommandEntry{Name: e.Args[0], Args: e.Args[1:]}
				command = limitedText(entry.Command(), maxLiveText)
			}
			out = append(out, slowEntry{at: e.Time, duration: e.Duration, command: command, client: client})
		}
		s.app.update(func() {
			if id != s.slowID || s.closed {
				return
			}
			s.slowLoading = false
			if err != nil {
				s.slowErr = err.Error()
				return
			}
			s.slowEntries, s.slowTotal = out, total
		})
	}()
}

func (s *session) resetSlowLog() {
	if !s.store.Connected() {
		return
	}
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		err := st.SlowLogReset(ctx)
		s.app.update(func() {
			if s.closed {
				return
			}
			if err != nil {
				s.setStatusErr(err)
				return
			}
			s.setStatus("Slow log reset")
			s.loadSlowLog()
		})
	}()
}

// runCLI runs one command from the CLI page.
func (s *session) runCLI() {
	line := strings.TrimSpace(s.cliInput)
	if line == "" || s.cliBusy || s.dbSwitching || s.closed {
		return
	}
	args, err := parseCommandLine(line)
	if err != nil {
		s.appendCLI(cliEntry{command: "[invalid command]", result: err.Error(), err: true})
		return
	}
	if len(args) == 0 {
		return
	}
	if sensitiveCommand(args[0], args[1:]) {
		line = strings.ToUpper(args[0]) + " [redacted]"
	} else {
		s.rememberCLI(line)
	}
	if strings.EqualFold(args[0], "SELECT") {
		if len(args) != 2 {
			s.appendCLI(cliEntry{command: line, result: "SELECT requires a database number", err: true})
			return
		}
		db, err := strconv.Atoi(args[1])
		if err != nil || db < 0 {
			s.appendCLI(cliEntry{command: line, result: "Invalid database number", err: true})
			return
		}
		s.app.protectEdits(s, func() {
			s.cliInput, s.cliBusy = "", true
			s.switchDBNow(db, func(err error) {
				s.cliBusy = false
				result := "OK"
				if err != nil {
					result = err.Error()
				}
				s.appendCLI(cliEntry{command: line, result: result, err: err != nil})
				s.tool = "cli"
			})
		})
		s.tool = "cli"
		return
	}
	if reason := cliBlocked(args[0]); reason != "" {
		s.appendCLI(cliEntry{command: line, result: reason, err: true})
		return
	}
	s.cliInput = ""
	s.cliBusy = true
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		value, err := st.Do(ctx, args...)
		result := formatReply(value)
		if err != nil {
			result = limitedText(err.Error(), maxCLIResult)
			if sensitiveCommand(args[0], args[1:]) {
				result = "command failed"
			}
		}
		s.app.update(func() {
			if s.closed {
				return
			}
			s.cliBusy = false
			s.appendCLI(cliEntry{command: line, result: result, err: err != nil})
		})
	}()
}

// cliBlocked explains why a stateful or streaming command has its own page.
func cliBlocked(name string) string {
	switch strings.ToUpper(name) {
	case "MONITOR", "SYNC", "PSYNC":
		return "Use the Monitor page."
	case "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE":
		return "Use the Pub/Sub page."
	case "MULTI", "EXEC", "DISCARD", "WATCH", "UNWATCH":
		return "Transactions are not supported in the CLI yet."
	case "AUTH", "HELLO", "RESET":
		return "Connection authentication and protocol changes belong in the connection settings."
	}
	return ""
}

func (s *session) appendCLI(e cliEntry) {
	e.command, e.result = limitedText(e.command, maxLiveText), limitedText(e.result, maxCLIResult)
	s.cliEntries = append(s.cliEntries, e)
	if len(s.cliEntries) > maxCLIEntries {
		s.cliEntries = append([]cliEntry(nil), s.cliEntries[len(s.cliEntries)-maxCLIEntries:]...)
	}
	if s.tool == "cli" {
		s.cliScroll.ScrollTo(0, math.MaxFloat32, ui.Smooth)
	}
}

func (s *session) clearCLI() { s.cliEntries = nil }

// startMonitor opens the live MONITOR stream on a dedicated connection.
func (s *session) startMonitor() {
	if s.closed || !s.store.Connected() || s.monitorRunning {
		return
	}
	s.stopMonitor()
	s.monitorID++
	id := s.monitorID
	ctx, cancel := context.WithCancel(context.Background())
	s.monitorCancel = cancel
	s.monitorRunning = true
	s.monitorErr = ""
	queue := &eventQueue[string]{limit: 128, post: s.app.update, apply: func(lines []string, dropped int) {
		if s.closed || id != s.monitorID {
			return
		}
		if dropped > 0 {
			lines = append([]string{s.app.tr("… messages omitted while the display was busy")}, lines...)
		}
		s.appendMonitors(lines)
	}}
	s.monitorEvents = queue
	st := s.store
	go func() {
		var budget liveBudget
		overloaded := false
		err := st.Monitor(ctx, func(line string) {
			if !budget.allow(len(line), time.Now()) {
				overloaded = true
				cancel()
				return
			}
			queue.push(limitedText(line, maxLiveText))
		})
		s.app.update(func() {
			if s.closed || id != s.monitorID {
				return
			}
			s.monitorRunning = false
			s.monitorCancel = nil
			if overloaded {
				s.monitorErr = s.app.tr("Live feed stopped: more than 4096 messages or 4 MiB per second.")
			}
			if err != nil && ctx.Err() == nil {
				s.monitorErr = err.Error()
			}
		})
	}()
}

func (s *session) stopMonitor() {
	s.monitorEvents.close()
	s.monitorID++
	if s.monitorCancel != nil {
		s.monitorCancel()
		s.monitorCancel = nil
	}
	s.monitorRunning = false
}

func (s *session) appendMonitors(lines []string) {
	for i := range lines {
		lines[i] = limitedText(lines[i], maxLiveText)
	}
	s.monitorLines = appendTail(s.monitorLines, lines, maxMonitorRows)
	if s.tool == "monitor" && s.monitorFollow {
		s.monitorScroll.ScrollTo(0, math.MaxFloat32, ui.Animation{Duration: time.Nanosecond})
	}
}

func (s *session) clearMonitor() { s.monitorLines = nil }

// startPubSub subscribes to the channels or patterns in the input.
func (s *session) startPubSub() {
	if s.closed || !s.store.Connected() {
		return
	}
	s.stopPubSub()
	channels := splitList(s.pubInput)
	if len(channels) == 0 {
		s.pubErr = "Enter a channel or pattern."
		return
	}
	s.pubID++
	id := s.pubID
	ctx, cancel := context.WithCancel(context.Background())
	ps := s.store.Subscribe(ctx, s.pubPatterns, channels...)
	if ps == nil {
		cancel()
		s.pubErr = "Not connected."
		return
	}
	s.pubCancel, s.pubSub = cancel, ps
	s.pubRunning = true
	s.pubErr = ""
	s.pubChannels = channels
	queue := &eventQueue[pubMessage]{limit: 128, post: s.app.update, apply: func(messages []pubMessage, dropped int) {
		if s.closed || id != s.pubID {
			return
		}
		if dropped > 0 {
			messages = append([]pubMessage{{at: time.Now(), payload: s.app.tr("… messages omitted while the display was busy")}}, messages...)
		}
		s.appendPubs(messages)
	}}
	s.pubEvents = queue
	go func() {
		defer ps.Close()
		var budget liveBudget
		for {
			msg, err := ps.ReceiveMessage(ctx)
			if err == nil && !budget.allow(len(msg.Channel)+len(msg.Pattern)+len(msg.Payload), time.Now()) {
				err = errors.New("Live feed stopped: more than 4096 messages or 4 MiB per second.")
			}
			if err != nil {
				s.app.update(func() {
					if s.closed || id != s.pubID {
						return
					}
					s.pubRunning = false
					if ctx.Err() == nil {
						s.pubErr = s.app.tr(err.Error())
					}
				})
				return
			}
			queue.push(pubMessage{at: time.Now(), channel: limitedText(msg.Channel, maxLiveText), pattern: limitedText(msg.Pattern, maxLiveText), payload: limitedText(msg.Payload, maxLiveText)})
		}
	}()
}

func (s *session) stopPubSub() {
	s.pubEvents.close()
	s.pubID++
	if s.pubCancel != nil {
		s.pubCancel()
		s.pubCancel = nil
	}
	if s.pubSub != nil {
		s.pubSub.Close()
		s.pubSub = nil
	}
	s.pubRunning = false
}

func (s *session) appendPubs(messages []pubMessage) {
	for i := range messages {
		msg := &messages[i]
		msg.channel, msg.pattern, msg.payload = limitedText(msg.channel, maxLiveText), limitedText(msg.pattern, maxLiveText), limitedText(msg.payload, maxLiveText)
	}
	s.pubMessages = appendTail(s.pubMessages, messages, maxPubRows)
	if s.tool == "pubsub" && s.pubFollow {
		s.pubScroll.ScrollTo(0, math.MaxFloat32, ui.Animation{Duration: time.Nanosecond})
	}
}

func (s *session) clearPub() { s.pubMessages = nil }

func (s *session) appendCommands(entries []CommandEntry) {
	s.cmdLog = appendTail(s.cmdLog, entries, maxCommandRows)
	if s.tool == "cmdlog" {
		s.cmdScroll.ScrollTo(0, math.MaxFloat32, ui.Animation{Duration: time.Nanosecond})
	}
}

func (s *session) clearCommandLog() { s.cmdLog = nil }

// stopLive stops monitor and pub/sub before leaving the tool or session.
func (s *session) stopLive() {
	s.stopMonitor()
	s.stopPubSub()
}

// slowLogSummary is the list's subtitle.
func (s *session) slowLogSummary() string {
	if s.slowTotal == 0 {
		return s.app.tr("No entries yet")
	}
	return s.app.tr("%d entries", s.slowTotal)
}

// clientRow is one CLIENT LIST entry.
type clientRow struct {
	addr, name, db, idle, cmd string
}

func parseClientList(raw string) []clientRow {
	var out []clientRow
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := map[string]string{}
		for _, part := range strings.Fields(line) {
			k, v, ok := strings.Cut(part, "=")
			if ok {
				fields[k] = v
			}
		}
		if fields["addr"] == "" {
			continue
		}
		out = append(out, clientRow{
			addr: fields["addr"], name: fields["name"], db: fields["db"],
			idle: fields["idle"], cmd: fields["cmd"],
		})
	}
	return out
}

func (s *session) loadClients() {
	if s.closed || !s.store.Connected() {
		return
	}
	s.clientsID++
	id := s.clientsID
	s.clientsLoading = true
	s.clientsErr = ""
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		raw, err := st.ClientList(ctx)
		s.app.update(func() {
			if id != s.clientsID || s.closed {
				return
			}
			s.clientsLoading = false
			if err != nil {
				s.clientsErr = err.Error()
				return
			}
			s.clients = parseClientList(raw)
		})
	}()
}

func (s *session) openClients() {
	s.clientsOpen = true
	s.loadClients()
}

func (s *session) publish() {
	channel := strings.TrimSpace(s.pubChannel)
	payload := s.pubPayload
	if channel == "" || s.pubPublishBusy || s.closed {
		return
	}
	s.pubPublishBusy = true
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		n, err := st.Publish(ctx, channel, payload)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.pubPublishBusy = false
			if err != nil {
				s.setStatusErr(err)
				return
			}
			s.pubPayload = ""
			s.setStatus("Published to %s · %d receivers", channel, n)
		})
	}()
}

func (s *session) filteredSlow() []slowEntry {
	q := strings.ToLower(strings.TrimSpace(s.slowFilter))
	if q == "" {
		return s.slowEntries
	}
	out := make([]slowEntry, 0, len(s.slowEntries))
	for _, e := range s.slowEntries {
		if strings.Contains(strings.ToLower(e.command+" "+e.client), q) {
			out = append(out, e)
		}
	}
	return out
}

func (s *session) filteredMonitor() []string {
	q := strings.ToLower(strings.TrimSpace(s.monitorFilter))
	if q == "" {
		return s.monitorLines
	}
	out := make([]string, 0, len(s.monitorLines))
	for _, line := range s.monitorLines {
		if strings.Contains(strings.ToLower(line), q) {
			out = append(out, line)
		}
	}
	return out
}

func (s *session) filteredPub() []pubMessage {
	q := strings.ToLower(strings.TrimSpace(s.pubFilter))
	if q == "" {
		return s.pubMessages
	}
	out := make([]pubMessage, 0, len(s.pubMessages))
	for _, m := range s.pubMessages {
		if strings.Contains(strings.ToLower(m.channel+" "+m.pattern+" "+m.payload), q) {
			out = append(out, m)
		}
	}
	return out
}

func (s *session) filteredCommands() []CommandEntry {
	q := strings.ToLower(strings.TrimSpace(s.cmdFilter))
	if q == "" {
		return s.cmdLog
	}
	out := make([]CommandEntry, 0, len(s.cmdLog))
	for _, e := range s.cmdLog {
		if strings.Contains(strings.ToLower(e.Command()), q) {
			out = append(out, e)
		}
	}
	return out
}

// filteredStream returns indexes into s.val.Stream for the stream filter.
func (s *session) filteredStream() []int {
	q := strings.ToLower(strings.TrimSpace(s.valQuery))
	out := make([]int, 0, len(s.val.Stream))
	for i, e := range s.val.Stream {
		if q == "" {
			out = append(out, i)
			continue
		}
		hay := e.ID
		for _, f := range e.Fields {
			hay += " " + f[0] + "=" + f[1]
		}
		if strings.Contains(strings.ToLower(hay), q) {
			out = append(out, i)
		}
	}
	return out
}

func (s *session) loadMoreStream() {
	if s.closed || s.valKey == "" || !s.streamMore || s.valLoadingMore {
		return
	}
	s.valLoadingMore = true
	id := s.valID
	key, before := s.valKey, s.streamCursor
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		entries, err := st.StreamBefore(ctx, key, before, valuePageSize)
		s.app.update(func() {
			if id != s.valID || s.valKey != key || s.closed {
				return
			}
			s.valLoadingMore = false
			if err != nil {
				s.setStatusErr(err)
				return
			}
			size, rows := valueFootprint(s.val)
			added, addedRows := valueFootprint(Value{Stream: entries})
			if rows+addedRows > maxLoadedRows || size+added > maxDecoded {
				s.streamMore = false
				s.setStatusErr(errValueLimit)
				return
			}
			for _, e := range entries {
				s.val.Stream = append(s.val.Stream, e)
				s.streamCursor = e.ID
			}
			s.streamMore = int64(len(s.val.Stream)) < s.val.Size
			s.val.Truncated = s.streamMore
		})
	}()
}

func (s *session) openStreamGroups() {
	if s.valKey == "" || s.val.Type != "stream" {
		return
	}
	s.groupsOpen = true
	s.loadStreamGroups()
}

func (s *session) loadStreamGroups() {
	if s.closed || s.valKey == "" || !s.store.Connected() {
		return
	}
	s.groupsID++
	id := s.groupsID
	key := s.valKey
	s.groupsLoading = true
	s.groupsErr = ""
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		groups, err := st.StreamGroups(ctx, key)
		s.app.update(func() {
			if id != s.groupsID || s.closed {
				return
			}
			s.groupsLoading = false
			if err != nil {
				s.groupsErr = err.Error()
				return
			}
			s.groups = groups
			s.val.Groups = int64(len(groups))
		})
	}()
}

func (s *session) createStreamGroup() {
	name := strings.TrimSpace(s.groupName)
	if name == "" || s.groupsBusy || s.closed || s.valKey == "" {
		return
	}
	s.groupsBusy = true
	key, start := s.valKey, strings.TrimSpace(s.groupStart)
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		err := st.StreamGroupCreate(ctx, key, name, start)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.groupsBusy = false
			if err != nil {
				s.groupsErr = err.Error()
				return
			}
			s.groupName = ""
			s.setStatus("Created consumer group %s", name)
			s.loadStreamGroups()
		})
	}()
}

func (s *session) destroyStreamGroup(name string) {
	if name == "" || s.groupsBusy || s.closed || s.valKey == "" {
		return
	}
	s.groupsBusy = true
	key := s.valKey
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		err := st.StreamGroupDestroy(ctx, key, name)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.groupsBusy = false
			if err != nil {
				s.groupsErr = err.Error()
				return
			}
			if s.groupSelected == name {
				s.groupSelected = ""
				s.groupConsumers = nil
				s.groupPending = nil
			}
			s.setStatus("Destroyed consumer group %s", name)
			s.loadStreamGroups()
		})
	}()
}

func (s *session) selectStreamGroup(name string) {
	s.groupSelected = name
	s.groupConsumers = nil
	s.groupPending = nil
	s.groupDetailErr = ""
	s.loadStreamGroupDetail(name)
}

func (s *session) loadStreamGroupDetail(group string) {
	if s.closed || s.valKey == "" || group == "" || !s.store.Connected() {
		return
	}
	s.groupDetailID++
	id := s.groupDetailID
	key := s.valKey
	s.groupDetailLoading = true
	s.groupDetailErr = ""
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		consumers, cErr := st.StreamConsumers(ctx, key, group)
		pending, pErr := st.StreamPendingPage(ctx, key, group, "-", 200)
		s.app.update(func() {
			if id != s.groupDetailID || s.closed || s.groupSelected != group {
				return
			}
			s.groupDetailLoading = false
			s.groupConsumers, s.groupPending = consumers, pending
			s.groupPendingMore = len(pending) == 200
			s.groupPendingCursor = ""
			if len(pending) > 0 {
				s.groupPendingCursor = pending[len(pending)-1].ID
			}
			if cErr != nil {
				s.groupDetailErr = cErr.Error()
			} else if pErr != nil {
				s.groupDetailErr = pErr.Error()
			}
		})
	}()
}

func (s *session) ackPending(id string) {
	if id == "" || s.groupSelected == "" || s.groupActionBusy || s.closed {
		return
	}
	s.groupActionBusy = true
	key, group := s.valKey, s.groupSelected
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		n, err := st.StreamAck(ctx, key, group, id)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.groupActionBusy = false
			if err != nil {
				s.groupDetailErr = err.Error()
				return
			}
			s.setStatus("Acknowledged %d pending entries", n)
			s.loadStreamGroupDetail(group)
			s.loadStreamGroups()
		})
	}()
}

func (s *session) claimPending(id string) {
	if id == "" || s.groupSelected == "" || s.groupActionBusy || s.closed {
		return
	}
	consumer := strings.TrimSpace(s.groupConsumer)
	if consumer == "" {
		s.groupDetailErr = "Enter a consumer name to claim to."
		return
	}
	s.groupActionBusy = true
	key, group := s.valKey, s.groupSelected
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		err := st.StreamClaim(ctx, key, group, consumer, id)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.groupActionBusy = false
			if err != nil {
				s.groupDetailErr = err.Error()
				return
			}
			s.setStatus("Claimed pending entry to %s", consumer)
			s.loadStreamGroupDetail(group)
			s.loadStreamGroups()
		})
	}()
}

func (s *session) loadMoreGroupPending() {
	if s.groupSelected == "" || s.groupPendingCursor == "" || !s.groupPendingMore || s.groupActionBusy || s.closed {
		return
	}
	s.groupActionBusy = true
	key, group, start := s.valKey, s.groupSelected, s.groupPendingCursor
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		pending, err := st.StreamPendingPage(ctx, key, group, start, 200)
		s.app.update(func() {
			if s.closed || s.groupSelected != group {
				return
			}
			s.groupActionBusy = false
			if err != nil {
				s.groupDetailErr = err.Error()
				return
			}
			size := 0
			for _, list := range [][]StreamPending{s.groupPending, pending} {
				for _, p := range list {
					size += len(p.ID) + len(p.Consumer) + 48
				}
			}
			if len(s.groupPending)+len(pending) > maxLoadedRows || size > maxDecoded {
				s.groupPendingMore = false
				s.groupDetailErr = errValueLimit.Error()
				return
			}
			s.groupPending = append(s.groupPending, pending...)
			if len(pending) > 0 {
				s.groupPendingCursor = pending[len(pending)-1].ID
			}
			s.groupPendingMore = len(pending) == 200
		})
	}()
}

func (s *session) deleteStreamConsumer(name string) {
	if name == "" || s.groupSelected == "" || s.groupActionBusy || s.closed {
		return
	}
	s.groupActionBusy = true
	key, group := s.valKey, s.groupSelected
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		n, err := st.StreamConsumerDelete(ctx, key, group, name)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.groupActionBusy = false
			if err != nil {
				s.groupDetailErr = err.Error()
				return
			}
			s.setStatus("Deleted consumer %s (%d pending)", name, n)
			s.loadStreamGroupDetail(group)
			s.loadStreamGroups()
		})
	}()
}

func (s *session) autoClaimPending() {
	if s.groupSelected == "" || s.groupActionBusy || s.closed {
		return
	}
	consumer := strings.TrimSpace(s.groupConsumer)
	if consumer == "" {
		s.groupDetailErr = "Enter a consumer name to claim to."
		return
	}
	minIdle, err := strconv.ParseInt(strings.TrimSpace(s.groupMinIdle), 10, 64)
	if err != nil || minIdle < 0 {
		s.groupDetailErr = "Min idle must be a number of milliseconds."
		return
	}
	s.groupActionBusy = true
	s.groupAutoClaiming = true
	key, group := s.valKey, s.groupSelected
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		n, next, err := st.StreamAutoClaim(ctx, key, group, consumer, time.Duration(minIdle)*time.Millisecond, "0-0", 200)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.groupActionBusy = false
			s.groupAutoClaiming = false
			if err != nil {
				s.groupDetailErr = err.Error()
				return
			}
			if next != "" {
				s.setStatus("Auto-claimed %d pending entries · %s", n, next)
			} else {
				s.setStatus("Auto-claimed %d pending entries", n)
			}
			s.loadStreamGroupDetail(group)
			s.loadStreamGroups()
		})
	}()
}

func (s *session) openStreamTrim() {
	if s.valKey == "" || s.val.Type != "stream" {
		return
	}
	s.trimOpen = true
	s.trimErr = ""
	if s.trimMode == "" {
		s.trimMode = "MAXLEN"
	}
}

func (s *session) trimStream() {
	if s.trimBusy || s.valKey == "" || s.closed {
		return
	}
	s.trimBusy = true
	s.trimErr = ""
	key, mode, value, approx := s.valKey, s.trimMode, s.trimValue, s.trimApprox
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		n, err := st.StreamTrim(ctx, key, mode, value, approx)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.trimBusy = false
			if err != nil {
				s.trimErr = err.Error()
				return
			}
			s.trimOpen = false
			s.setStatus("Trimmed %d stream entries", n)
			s.reloadValue()
			s.loadStreamGroups()
		})
	}()
}

func (s *session) rememberCLI(line string) {
	args, err := parseCommandLine(line)
	if err != nil || len(args) == 0 || sensitiveCommand(args[0], args[1:]) || len(line) > maxLiveText {
		return
	}
	if len(s.cliHistory) == 0 || s.cliHistory[len(s.cliHistory)-1] != line {
		s.cliHistory = append(s.cliHistory, line)
	}
	if len(s.cliHistory) > maxCLIEntries {
		s.cliHistory = s.cliHistory[len(s.cliHistory)-maxCLIEntries:]
	}
	s.cliHistoryPos, s.cliDraft = len(s.cliHistory), ""
}

func (s *session) browseCLI(delta int) {
	if len(s.cliHistory) == 0 {
		return
	}
	if s.cliHistoryPos == len(s.cliHistory) {
		s.cliDraft = s.cliInput
	}
	s.cliHistoryPos = max(0, min(len(s.cliHistory), s.cliHistoryPos+delta))
	if s.cliHistoryPos == len(s.cliHistory) {
		s.cliInput = s.cliDraft
	} else {
		s.cliInput = s.cliHistory[s.cliHistoryPos]
	}
}
