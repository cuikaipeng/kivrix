package app

// A session is one open connection: its client, keyspace, key scan and
// value panel. The app keeps several, shown as tabs, and the active one
// builds the sidebar and the working card.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/redis/go-redis/v9"
)

const (
	scanPageSize  = 1000
	valuePageSize = 200
)

type session struct {
	app *App
	id  string

	cfg   ConnConfig
	store Store
	info  string
	db    int

	keys                       *keyTree
	outline                    ui.OutlineState[string]
	sel                        ui.Selection[string]
	search                     string
	keyMatch, keyType, keyView string
	focusSearch                bool
	checkMode                  bool
	scanning                   bool

	scanID         uint64
	scanCancel     context.CancelFunc
	scanTimer      *time.Timer
	scanIncomplete bool
	scanFilter     keyFilter

	dbChoices   []string
	dbChoice    string
	dbKeys      map[int]int64
	dbGen       uint64
	dbSwitching bool

	val                  Value
	valKey               string
	valLoading           bool
	valueSaving          bool
	keyBusy              bool
	valLoadingMore       bool
	valErr               string
	valID                uint64
	valQuery             string
	valFormat            string
	valDecode            string
	valCursor            uint64
	valOffset            int
	valMore              bool
	valIndices           []int
	valRowSel            int
	valTimer             *time.Timer
	valCancel            context.CancelFunc
	strEditor            editor.State
	decodedEditor        editor.State
	editCodec            valueCodec
	decodedID            uint64
	decodedBusy          bool
	decodedErr           string
	decodedCancel        context.CancelFunc
	valRows              ui.ListState
	valSort, lastValSort ui.SortOrder
	valueFull            bool
	cellCodec            valueCodec
	cellKey              uint64
	cellCache            map[string]string
	cellPending          map[string]bool
	cellCancel           context.CancelFunc
	cellContext          context.Context
	cellWorkers          chan struct{}

	status    message
	statusErr bool
	closed    bool

	// tool is the working card's page: browser or one of the server tools.
	tool string

	statusInfo     map[string]map[string]string
	statusRaw      string
	statusLoading  bool
	infoErr        string
	statusID       uint64
	statusUpdated  time.Time
	statusHistory  []statusSample
	statusSampling bool
	statusTimer    *time.Timer

	cliHistory    []string
	cliHistoryPos int
	cliDraft      string
	cliHandle     ui.Handle
	cliEntries    []cliEntry
	cliInput      string
	cliBusy       bool
	cliScroll     ui.ScrollState
	cliRows       ui.ListState

	slowEntries     []slowEntry
	slowTotal       int64
	slowLoading     bool
	slowErr         string
	slowID          uint64
	slowRows        ui.ListState
	slowFilter      string
	slowLimitChoice string

	clients        []clientRow
	clientsLoading bool
	clientsErr     string
	clientsID      uint64
	clientsOpen    bool
	clientsRows    ui.ListState

	monitorLines   []string
	monitorFilter  string
	monitorFollow  bool
	monitorID      uint64
	monitorRunning bool
	monitorErr     string
	monitorCancel  context.CancelFunc
	monitorScroll  ui.ScrollState
	monitorRows    ui.ListState
	monitorEvents  *eventQueue[string]

	pubInput       string
	pubFilter      string
	pubFollow      bool
	pubChannel     string
	pubPayload     string
	pubPublishBusy bool
	pubID          uint64
	pubPatterns    bool
	pubRunning     bool
	pubChannels    []string
	pubMessages    []pubMessage
	pubRows        ui.ListState
	pubErr         string
	pubCancel      context.CancelFunc
	pubSub         *redis.PubSub
	pubScroll      ui.ScrollState
	pubEvents      *eventQueue[pubMessage]

	cmdLog    []CommandEntry
	cmdFilter string
	cmdScroll ui.ScrollState
	cmdRows   ui.ListState
	cmdHook   *commandHook
	cmdEvents *eventQueue[CommandEntry]

	streamMore    bool
	streamCursor  string
	streamIndices []int

	groupsOpen         bool
	groups             []StreamGroup
	groupsLoading      bool
	groupsErr          string
	groupsID           uint64
	groupsBusy         bool
	groupName          string
	groupStart         string
	groupSelected      string
	groupConsumers     []StreamConsumer
	groupPending       []StreamPending
	groupDetailLoading bool
	groupDetailErr     string
	groupDetailID      uint64
	groupConsumer      string
	groupActionBusy    bool
	groupPendingMore   bool
	groupPendingCursor string
	groupAutoClaiming  bool
	groupMinIdle       string

	trimOpen   bool
	trimMode   string
	trimValue  string
	trimApprox bool
	trimBusy   bool
	trimErr    string

	opsSaving bool

	autoRefresh bool
	autoTimer   *time.Timer
	autoEvery   time.Duration

	ioBusy                     bool
	ioCancel                   context.CancelFunc
	ioStarted                  time.Time
	ioElapsed                  time.Duration
	ioDone, ioSkipped, ioTotal int
	ioLabel                    string
	ioTimer                    *time.Timer
	ioProgress                 *eventQueue[transferProgress]
}

// rowEdit is one add or edit of a collection row.
type rowEdit struct {
	key, typ, mode    string
	streamID          string
	index             int
	field, oldField   string
	value             string
	member, oldMember string
	score             string
	err               string
	busy              bool
	loading           bool
	codec             valueCodec
	bulk, head        bool
	bulkText          string
}

func newSession(a *App, p Profile, st Store, info string) *session {
	s := &session{
		app:             a,
		id:              p.ID,
		cfg:             st.cfg,
		store:           st,
		info:            info,
		db:              st.cfg.DB,
		keys:            newKeyTree(keyTreeDelim),
		dbChoice:        st.cfg.dbLabel(st.cfg.DB),
		valFormat:       a.prefs.DefaultFormat,
		valDecode:       a.prefs.DefaultDecode,
		valRowSel:       -1,
		tool:            "browser",
		slowLimitChoice: "128",
		monitorFollow:   true,
		pubFollow:       true,
		groupStart:      "$",
		groupConsumer:   "kivrix",
		groupMinIdle:    "60000",
		trimMode:        "MAXLEN",
	}
	s.keyMatch, s.keyType, s.keyView = "Contains", "All types", s.cfg.KeyView
	if s.keyView == "" {
		s.keyView = "Tree"
	}
	if s.cfg.DefaultFilter != "" {
		s.search, s.keyMatch = s.cfg.DefaultFilter, "Pattern"
	}
	s.setKeyView()
	s.scanFilter = s.keyFilter()
	s.cmdEvents = &eventQueue[CommandEntry]{limit: 128, post: a.update, apply: func(entries []CommandEntry, dropped int) {
		if s.closed {
			return
		}
		s.appendCommands(entries)
	}}
	s.cmdHook = &commandHook{record: s.cmdEvents.push}
	st.AddHook(s.cmdHook)
	s.outline.List.Selection = &s.sel
	s.valRows.Selected = &s.valRowSel
	s.valRows.Sort = &s.valSort
	s.valRows.CellMenu = func(m *ui.Menu, row, col int) {
		if s.val.Type == "stream" {
			if m.Item("Delete Entry…").Disabled(s.cfg.ReadOnly).Chosen() {
				a.askDeleteRow(s, row)
			}
			return
		}
		if m.Item("Edit…").Disabled(s.cfg.ReadOnly).Chosen() {
			a.openRowEdit(s, row)
		}
		if m.Item("Delete…").Disabled(s.cfg.ReadOnly).Chosen() {
			a.askDeleteRow(s, row)
		}
	}
	s.dbChoices = []string{s.dbChoice}
	return s
}

func (s *session) name() string { return s.cfg.label() }

// toggleCheckMode switches between normal selection and click-to-check.
func (s *session) toggleCheckMode() {
	s.checkMode = !s.checkMode
	if s.checkMode {
		s.outline.List.Selection = nil
		s.setStatus("Check mode: click keys to select")
	} else {
		s.outline.List.Selection = &s.sel
		s.setStatus("Check mode off")
	}
}

func (s *session) close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.app.rowSessionID == s.id && s.app.rowCancel != nil {
		s.app.rowCancel()
	}
	if s.decodedCancel != nil {
		s.decodedCancel()
	}
	s.cmdEvents.close()
	s.endTransfer()
	if s.valCancel != nil {
		s.valCancel()
	}
	if s.cellCancel != nil {
		s.cellCancel()
	}
	if s.ioCancel != nil {
		s.ioCancel()
		s.ioCancel = nil
	}
	s.stopLive()
	if s.scanTimer != nil {
		s.scanTimer.Stop()
		s.scanTimer = nil
	}
	if s.scanCancel != nil {
		s.scanCancel()
		s.scanCancel = nil
	}
	if s.valTimer != nil {
		s.valTimer.Stop()
		s.valTimer = nil
	}
	if s.autoTimer != nil {
		s.autoTimer.Stop()
		s.autoTimer = nil
	}
	if s.statusTimer != nil {
		s.statusTimer.Stop()
		s.statusTimer = nil
	}
	s.store.Close()
}

func (s *session) setStatus(key string, args ...any) {
	s.status, s.statusErr = msg(key, args...), false
}

func (s *session) setStatusErr(err error) {
	if err == nil {
		return
	}
	s.status, s.statusErr = errorMessage(err), true
}

func (s *session) clearStatus() {
	s.status, s.statusErr = message{}, false
}

// scanPattern turns the filter into a SCAN glob, empty for every key.
func scanPattern(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return "*"
	}
	return "*" + q + "*"
}

func (s *session) scheduleScan() {
	if !s.store.Connected() {
		return
	}
	if s.scanTimer != nil {
		s.scanTimer.Stop()
	}
	q := s.search
	s.scanTimer = time.AfterFunc(scanDebounce, func() {
		s.app.update(func() {
			if s.search == q {
				s.startScan()
			}
		})
	})
}

func (s *session) rescan() {
	s.startScan()
	s.refreshDatabases()
}

// startScan loads the matching keyspace in cancellable background batches.
func (s *session) startScan() {
	filter := s.keyFilter()
	if s.hasEdits() {
		s.search, s.keyMatch, s.keyType = s.scanFilter.query, s.scanFilter.mode, s.scanFilter.typ
	}
	s.app.protectEdits(s, func() {
		s.search, s.keyMatch, s.keyType = filter.query, filter.mode, filter.typ
		s.startScanNow()
	})
}

func (s *session) startScanNow() {
	if !s.store.Connected() || s.closed || s.dbSwitching {
		return
	}
	s.cancelScan()
	s.scanID++
	id := s.scanID
	s.keys.reset()
	s.sel.Clear()
	s.clearValue()
	s.scanFilter = s.keyFilter()
	s.scanIncomplete = true
	s.scanning = true
	s.clearStatus()
	ctx, cancel := context.WithCancel(context.Background())
	s.scanCancel = cancel
	if s.search != "" {
		s.setStatus("Searching all matching keys…")
	}
	st, filter, count := s.store, s.scanFilter, s.scanSize()
	go func() {
		defer cancel()
		// Keep one batch in flight and yield between requests so large scans
		// do not flood Redis or queue unbounded UI work.
		pace := time.NewTicker(50 * time.Millisecond)
		defer pace.Stop()
		applied := make(chan bool, 1)
		cursor := ""
		for ctx.Err() == nil {
			keys, next, err := st.scanFiltered(ctx, cursor, filter, count)
			s.app.update(func() {
				if id != s.scanID || s.closed || ctx.Err() != nil {
					applied <- false
					return
				}
				if err != nil {
					s.cancelScan()
					s.setStatusErr(err)
					applied <- false
					return
				}
				if !s.applyScanBatch(id, keys) {
					applied <- false
					return
				}
				if next == "" {
					s.scanning, s.scanIncomplete = false, false
					s.scanCancel = nil
					if filter.query != "" {
						s.setStatus("Found %d keys", s.keys.count)
					}
				}
				applied <- true
			})
			select {
			case ok := <-applied:
				if !ok {
					return
				}
			case <-ctx.Done():
				return
			}
			if next == "" {
				return
			}
			cursor = next
			select {
			case <-ctx.Done():
				return
			case <-pace.C:
			}
		}
	}()
}

func (s *session) stopScan() {
	s.cancelScan()
	s.scanID++
	s.scanning = false
	s.setStatus("Scan canceled; loaded keys are kept")
}

func (s *session) cancelScan() {
	if s.scanTimer != nil {
		s.scanTimer.Stop()
		s.scanTimer = nil
	}
	if s.scanCancel != nil {
		s.scanCancel()
		s.scanCancel = nil
	}
	s.scanning = false
}

// applyScanBatch adds a page only while it belongs to the current scan.
func (s *session) applyScanBatch(id uint64, keys []string) bool {
	if id != s.scanID {
		return false
	}
	for _, k := range keys {
		if !s.keys.add(k) {
			s.cancelScan()
			s.scanIncomplete = true
			s.setStatusErr(errors.New("loaded key limit reached; narrow the filter and rescan"))
			return false
		}
	}
	return true
}

// watchSelection loads the value of a newly selected key and clears the
// value panel when a branch or nothing is selected.
func (s *session) watchSelection() {
	if s.app.editGuard != nil && s.app.editGuard.session == s {
		return
	}
	if s.hasEdits() && !s.sel.Has(s.valKey) {
		var selected []string
		for key := range s.sel.All() {
			selected = append(selected, key)
		}
		s.sel.Clear()
		s.sel.Add(s.valKey)
		s.app.protectEdits(s, func() {
			s.sel.Clear()
			for _, key := range selected {
				s.sel.Add(key)
			}
			s.watchSelection()
		})
		return
	}
	if s.sel.Len() > 1 {
		// A batch is chosen: keep the value of the key in it, if the value
		// shown is one of them, rather than jumping among the choice.
		if s.valKey != "" && s.sel.Has(s.valKey) {
			return
		}
		s.clearValue()
		return
	}
	selected := ""
	for k := range s.sel.All() {
		selected = k
		break
	}
	if selected == s.valKey {
		return
	}
	if selected == "" {
		if s.valKey != "" {
			s.clearValue()
		}
		return
	}
	if n := s.keys.node(selected); n == nil || !n.isKey {
		if s.valKey != "" {
			s.clearValue()
		}
		return
	}
	if s.tool != "browser" {
		s.setTool("browser")
	}
	s.loadValue(selected)
}

func (s *session) loadValue(key string) {
	s.app.protectEdits(s, func() { s.loadValueNow(key) })
}

func (s *session) loadValueNow(key string) {
	if s.decodedCancel != nil {
		s.decodedCancel()
	}
	s.valueSaving = false
	s.valID++
	id := s.valID
	s.valKey = key
	s.valLoading = true
	s.valLoadingMore = false
	s.valErr = ""
	s.val = Value{}
	s.valQuery = ""
	s.valCursor = 0
	s.valOffset = 0
	s.valMore = false
	s.valIndices = nil
	s.valRowSel = -1
	s.decodedID++
	s.decodedBusy = false
	s.decodedErr = ""
	s.streamMore = false
	s.streamCursor = ""
	s.streamIndices = nil
	s.groupsOpen = false
	s.groups = nil
	s.groupsID++
	if s.valTimer != nil {
		s.valTimer.Stop()
		s.valTimer = nil
	}
	s.fetchValue(id, 0, 0, false)
}

// valueMatch is the SCAN filter for the loaded type.
func (s *session) valueMatch() string {
	if s.val.Type == "string" || s.valQuery == "" {
		return ""
	}
	return "*" + s.valQuery + "*"
}

// fetchValue loads one page; more appends, and is false only on reload.
func (s *session) fetchValue(id uint64, cursor uint64, offset int, more bool) {
	key, st, match, pageSize := s.valKey, s.store, s.valueMatch(), s.valuePageSize()
	if s.valCancel != nil {
		s.valCancel()
	}
	ctx, cancel := st.commandContext()
	s.valCancel = cancel
	go func() {
		defer cancel()
		p, err := st.GetValuePage(ctx, key, cursor, offset, pageSize, match)
		if !more && errors.Is(err, errValueLimit) && p.Type == "string" {
			p, err = st.stringPreview(ctx, key)
		}
		s.app.update(func() {
			if id != s.valID || s.valKey != key || s.closed {
				return
			}
			s.valLoading, s.valLoadingMore = false, false
			if err != nil {
				if more {
					s.setStatusErr(err)
				} else {
					s.valErr = err.Error()
				}
				return
			}
			s.applyValuePage(p, more)
		})
	}()
}

// applyValuePage stores a page and loads a string into the editor.
func (s *session) applyValuePage(p ValuePage, more bool) {
	size, rows := valueFootprint(p.Value)
	if more {
		oldSize, oldRows := valueFootprint(s.val)
		size, rows = size+oldSize, rows+oldRows
	}
	if size > maxDecoded || rows > maxLoadedRows || len(p.Str) > maxEditorBytes {
		s.valMore, s.streamMore = false, false
		if more {
			s.setStatusErr(errValueLimit)
		} else {
			s.valErr = errValueLimit.Error()
		}
		return
	}
	if more {
		s.val.Pairs = append(s.val.Pairs, p.Pairs...)
		s.val.Items = append(s.val.Items, p.Items...)
		s.valIndices = append(s.valIndices, p.ListIndices...)
	} else {
		s.val = p.Value
		s.valIndices = p.ListIndices
	}
	if p.Type == "stream" {
		if more {
			s.val.Stream = append(s.val.Stream, p.Stream...)
		}
		if len(s.val.Stream) > 0 {
			s.streamCursor = s.val.Stream[len(s.val.Stream)-1].ID
		}
		s.streamMore = int64(len(s.val.Stream)) < s.val.Size
		s.streamIndices = nil
	}
	s.val.Size = p.Size
	s.val.TTL = p.TTL
	s.val.Type = p.Type
	s.val.Truncated = p.HasMore || p.Truncated
	s.valCursor = p.NextCursor
	s.valOffset = p.NextOffset
	s.valMore = p.HasMore
	s.sortHashRows()
	if p.Type == "none" && s.valKey != "" {
		// Removing the last field or member deletes the key on the server.
		s.keys.remove(s.valKey)
		s.sel.Remove(s.valKey)
		s.setStatus("Key no longer exists")
	}
	if p.Type == "string" || p.Type == "json" {
		if s.strEditor.Buffer == nil {
			s.strEditor.Buffer = editor.NewBuffer(p.Str)
		} else {
			s.strEditor.Buffer.Reset(p.Str)
		}
		s.strEditor.Buffer.MarkSaved()
		s.strEditor.Buffer.SetLanguage(sniffLanguage(p.Str))
		if p.PreviewOnly {
			s.valFormat, s.valDecode = "Raw", "None"
		} else {
			s.prepareDecodedEditor(p.Str)
		}
	}
	if !more && p.Type != "none" {
		s.app.rememberKey(s)
	}
}

// reloadValue starts the value again at its first page, for a new filter.
func (s *session) reloadValue() {
	s.app.protectEdits(s, func() { s.reloadValueNow() })
}

func (s *session) reloadValueNow() {
	if s.valKey == "" || s.closed {
		return
	}
	if s.decodedCancel != nil {
		s.decodedCancel()
	}
	s.valueSaving = false
	s.valID++
	id := s.valID
	typ := s.val.Type
	s.valLoading = true
	s.valLoadingMore = false
	s.valErr = ""
	s.val = Value{Type: typ}
	s.valCursor = 0
	s.valOffset = 0
	s.valMore = false
	s.valIndices = nil
	s.valRowSel = -1
	s.decodedID++
	s.decodedBusy = false
	s.decodedErr = ""
	s.streamMore = false
	s.streamCursor = ""
	s.streamIndices = nil
	s.fetchValue(id, 0, 0, false)
}

func (s *session) scheduleValueReload() {
	if s.valTimer != nil {
		s.valTimer.Stop()
	}
	q := s.valQuery
	s.valTimer = time.AfterFunc(250*time.Millisecond, func() {
		s.app.update(func() {
			if s.valQuery == q {
				s.reloadValue()
			}
		})
	})
}

func (s *session) loadMoreValue() {
	if s.valKey == "" || !s.valMore || s.valLoading || s.valLoadingMore || s.closed {
		return
	}
	s.valLoadingMore = true
	s.fetchValue(s.valID, s.valCursor, s.valOffset, true)
}

func (s *session) clearValue() {
	if s.decodedCancel != nil {
		s.decodedCancel()
	}
	s.valueSaving = false
	if s.valCancel != nil {
		s.valCancel()
		s.valCancel = nil
	}
	if s.cellCancel != nil {
		s.cellCancel()
	}
	if s.valTimer != nil {
		s.valTimer.Stop()
		s.valTimer = nil
	}
	s.valID++
	s.valKey = ""
	s.val = Value{}
	s.valLoading = false
	s.valLoadingMore = false
	s.valErr = ""
	s.valQuery = ""
	s.valCursor = 0
	s.valOffset = 0
	s.valMore = false
	s.valIndices = nil
	s.valRowSel = -1
	s.decodedID++
	s.decodedBusy = false
	s.decodedErr = ""
	s.streamMore = false
	s.streamCursor = ""
	s.streamIndices = nil
	s.groupsOpen = false
	s.groups = nil
	s.groupsID++
}

func (s *session) saveString(done ...func(error)) {
	if s.encodedEditorActive() {
		s.saveEncodedString(done...)
		return
	}
	s.saveValueBuffer(&s.strEditor, valueCodec{}, done...)
}

func (s *session) saveValueBuffer(state *editor.State, codec valueCodec, callbacks ...func(error)) {
	done := func(err error) {
		for _, fn := range callbacks {
			fn(err)
		}
	}
	if !s.writable() || s.val.PreviewOnly {
		done(errReadOnly)
		return
	}
	buf := state.Buffer
	if buf == nil || s.valueSaving || s.closed || s.valKey == "" {
		done(errSaveObsolete)
		return
	}
	s.valueSaving = true
	key, text, typ := s.valKey, buf.String(), s.val.Type
	version, id, st := buf.Version(), s.valID, s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		encoded, err := codec.save(text)
		if err == nil {
			if typ == "json" {
				err = st.JSONSet(ctx, key, string(encoded))
			} else {
				err = st.SetString(ctx, key, string(encoded))
			}
		}
		s.app.update(func() {
			if s.closed || id != s.valID || s.valKey != key || state.Buffer != buf {
				done(errSaveObsolete)
				return
			}
			s.valueSaving = false
			if err != nil {
				done(err)
				s.setStatusErr(err)
				return
			}
			if buf.Version() == version {
				buf.MarkSaved()
			}
			s.val.Str, s.val.Size = string(encoded), int64(len(encoded))
			if state != &s.strEditor && s.strEditor.Buffer != nil {
				s.strEditor.Buffer.Reset(string(encoded))
				s.strEditor.Buffer.MarkSaved()
			}
			s.setStatus("Saved %s", key)
			done(nil)
		})
	}()
}

func (s *session) deleteKey(key string) {
	s.keyBusy = true
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		n, err := st.DeleteKeys(ctx, key)
		s.app.update(func() {
			s.keyBusy = false
			if s.closed {
				return
			}
			if err != nil {
				s.setStatusErr(err)
				return
			}
			s.keys.remove(key)
			if s.valKey == key {
				s.clearValue()
			}
			s.sel.Remove(key)
			if n == 0 {
				s.setStatus("Key %s was already gone", key)
			} else {
				s.setStatus("Deleted %s", key)
			}
		})
	}()
}

func (s *session) deleteKeys(keys []string) {
	if len(keys) == 0 {
		return
	}
	s.keyBusy = true
	st := s.store
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n, err := st.DeleteKeys(ctx, keys...)
		s.app.update(func() {
			s.keyBusy = false
			if s.closed {
				return
			}
			if err != nil {
				s.setStatusErr(err)
				return
			}
			for _, k := range keys {
				s.keys.remove(k)
				s.sel.Remove(k)
				if s.valKey == k {
					s.clearValue()
				}
			}
			s.setStatus("Deleted %d keys", n)
		})
	}()
}

// deleteNamespace deletes every key on the server under the namespace, not
// only the keys the browser has scanned so far.
func (s *session) deleteNamespace(path string) {
	if !s.writable() || s.ioBusy {
		return
	}
	s.ioBusy = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	s.ioCancel = cancel
	report := s.beginTransfer("Deleting", 0)
	prefix := s.namespacePrefix(path)
	st := s.store
	s.setStatus("Deleting keys under %s…", prefix)
	go func() {
		defer cancel()
		n, err := st.DeletePrefix(ctx, prefix, func(n int64) { report(int(n), 0) })
		s.app.update(func() {
			if s.closed {
				return
			}
			s.endTransfer()
			if err != nil {
				s.startScan()
				if errors.Is(err, context.Canceled) {
					s.setStatus("Deletion canceled: %d deleted. Completed deletions remain.", n)
				} else {
					s.setStatusErr(localError("Deleted %d keys before stopping: %w", n, err))
				}
				return
			}
			var gone []string
			for p, node := range s.keys.byPath {
				if node.isKey && strings.HasPrefix(p, prefix) {
					gone = append(gone, p)
				}
			}
			for _, k := range gone {
				s.keys.remove(k)
			}
			if strings.HasPrefix(s.valKey, prefix) {
				s.clearValue()
			}
			s.sel.Clear()
			if s.scanning {
				s.startScan()
			}
			s.setStatus("Deleted %d keys under %s", n, prefix)
			s.refreshDatabases()
		})
	}()
}

// refreshDatabases reads the keyspace counts and the number of databases.
func (s *session) refreshDatabases() {
	if !s.store.Connected() || s.closed {
		return
	}
	s.dbGen++
	gen := s.dbGen
	curDB := s.db
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		n, _ := st.NumDatabases(ctx)
		if n <= 0 {
			n = 16
		}
		if st.IsCluster() {
			n = 1
		}
		n = min(n, maxLoadedRows)
		keys, _ := st.Keyspace(ctx)
		s.app.update(func() {
			if gen != s.dbGen || s.closed {
				return
			}
			s.dbChoices = s.dbChoices[:0]
			for i := 0; i < n; i++ {
				if s.cfg.showsDB(i) || i == s.db {
					s.dbChoices = append(s.dbChoices, s.cfg.dbLabel(i))
				}
			}
			if curDB >= n {
				s.dbChoices = append(s.dbChoices, s.cfg.dbLabel(curDB))
			}
			s.dbKeys = keys
			s.dbChoice = s.cfg.dbLabel(s.db)
		})
	}()
}

func (s *session) selectDB() {
	db, err := strconv.Atoi(dbNumber(s.dbChoice))
	if err != nil || db == s.db {
		return
	}
	if s.dbSwitching {
		s.dbChoice = s.cfg.dbLabel(s.db)
		return
	}
	s.switchDB(db)
}

// switchDB reconnects the client in db, then browses its keyspace afresh.
func (s *session) switchDB(db int, callbacks ...func(error)) {
	s.dbChoice = s.cfg.dbLabel(s.db)
	s.app.protectEdits(s, func() { s.switchDBNow(db, callbacks...) })
}

func (s *session) switchDBNow(db int, callbacks ...func(error)) {
	done := func(err error) {
		for _, fn := range callbacks {
			fn(err)
		}
	}
	if s.ioBusy || s.keyBusy {
		done(localError("Wait for the current transfer or deletion to finish."))
		return
	}
	if db == s.db {
		done(nil)
		return
	}
	if db < 0 || s.closed || s.store.IsCluster() {
		done(errors.New("database is unavailable"))
		return
	}
	s.stopLive()
	s.tool = "browser"
	s.cancelScan()
	s.scanID++
	s.scanning = false
	s.dbSwitching = true
	s.setStatus("Switching to db%d…", db)
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		err := st.SelectDB(ctx, db)
		s.app.update(func() {
			if s.closed {
				return
			}
			s.dbSwitching = false
			if err != nil {
				s.setStatusErr(err)
				s.dbChoice = s.cfg.dbLabel(s.db)
				done(err)
				return
			}
			st.AddHook(s.cmdHook)
			s.store = st
			s.cfg.DB = db
			s.db = db
			s.dbChoice = s.cfg.dbLabel(db)
			s.search = s.cfg.DefaultFilter
			if s.search != "" {
				s.keyMatch = "Pattern"
			}
			s.keys.reset()
			s.sel.Clear()
			s.clearValue()
			s.startScan()
			s.refreshDatabases()
			s.setStatus("Switched to db%d", db)
			done(nil)
		})
	}()
}

func (s *session) dbOptionLabel(choice string) string {
	db, err := strconv.Atoi(dbNumber(choice))
	if err != nil {
		return choice
	}
	count := "?"
	if s.dbKeys != nil {
		// INFO keyspace omits empty databases; nil means statistics are unavailable.
		count = strconv.FormatInt(s.dbKeys[db], 10)
	}
	label := fmt.Sprintf("db%d (%s)", db, count)
	if alias := s.cfg.DBAliases[db]; alias != "" {
		label += " · " + alias
	}
	return label
}

func formatCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func (s *session) saveEncodedString(done ...func(error)) {
	codec := s.editCodec
	if codec == (valueCodec{}) {
		codec = s.valueCodec()
	}
	s.saveValueBuffer(&s.decodedEditor, codec, done...)
}

// saveRow writes one row and reloads the value.
func (s *session) saveRow(r rowEdit, done func(error)) {
	var score float64
	if r.typ == "zset" && !r.bulk {
		var err error
		score, err = strconv.ParseFloat(strings.TrimSpace(r.score), 64)
		if err != nil {
			done(fmt.Errorf("score: %w", err))
			return
		}
	}
	st, generation, valueID := s.store, s.app.rowGeneration, s.valID
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		var err error
		if r.typ != "stream" && !r.bulk {
			text := r.value
			if r.typ == "set" || r.typ == "zset" {
				text = r.member
			}
			var encoded []byte
			encoded, err = r.codec.save(text)
			if err != nil {
				s.app.update(func() {
					if !s.closed {
						done(err)
					}
				})
				return
			}
			if r.typ == "set" || r.typ == "zset" {
				r.member = string(encoded)
			} else {
				r.value = string(encoded)
			}
		}
		if r.bulk {
			err = st.addRows(ctx, r)
		} else {
			switch r.typ {
			case "hash":
				err = st.HashSet(ctx, r.key, r.oldField, r.field, r.value)
			case "list":
				if r.mode == "add" {
					if r.head {
						err = st.client.LPush(ctx, r.key, r.value).Err()
					} else {
						err = st.ListPush(ctx, r.key, r.value)
					}
				} else {
					err = st.ListSet(ctx, r.key, r.index, r.value)
				}
			case "set":
				if r.mode == "add" {
					err = st.SetAdd(ctx, r.key, r.member)
				} else {
					err = st.SetReplace(ctx, r.key, r.oldMember, r.member)
				}
			case "zset":
				if r.mode == "add" {
					err = st.ZSetAdd(ctx, r.key, r.member, score)
				} else {
					err = st.ZSetReplace(ctx, r.key, r.oldMember, r.member, score)
				}
			case "stream":
				var fields [][2]string
				fields, err = parseStreamFields(r.value)
				if err == nil {
					_, err = st.StreamAdd(ctx, r.key, r.streamID, fields)
				}
			}
		}
		s.app.update(func() {
			if s.closed {
				return
			}
			if err != nil {
				done(err)
				return
			}
			if s.app.rowOpen && s.app.rowSessionID == s.id && s.app.rowGeneration == generation {
				s.app.row.oldField, s.app.row.oldMember = r.field, r.member
			}
			if s.valID == valueID && s.valKey == r.key {
				s.reloadValue()
			}
			s.setStatus("Saved %s row", r.typ)
			done(nil)
		})
	}()
}

// deleteRow removes one row and reloads the value.
func (s *session) deleteRow(r rowEdit, done func(error)) {
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		var err error
		switch r.typ {
		case "hash":
			err = st.HashDel(ctx, r.key, r.field)
		case "list":
			err = st.ListDel(ctx, r.key, r.index)
		case "set":
			err = st.SetDel(ctx, r.key, r.member)
		case "zset":
			err = st.ZSetDel(ctx, r.key, r.member)
		case "stream":
			err = st.StreamDel(ctx, r.key, r.field)
		}
		s.app.update(func() {
			if s.closed {
				return
			}
			if err != nil {
				done(err)
				return
			}
			s.reloadValue()
			s.setStatus("Deleted %s row", r.typ)
			done(nil)
		})
	}()
}

// toggleAutoRefresh reloads the current value every five seconds.
func (s *session) toggleAutoRefresh() {
	if s.autoRefresh {
		s.autoRefresh = false
		if s.autoTimer != nil {
			s.autoTimer.Stop()
			s.autoTimer = nil
		}
		s.setStatus("Auto refresh off")
		return
	}
	s.autoRefresh = true
	if s.autoEvery <= 0 {
		s.autoEvery = s.app.prefs.refreshInterval()
	}
	s.scheduleAutoRefresh()
	s.setStatus("Auto refresh every %gs", s.autoEvery.Seconds())
}

func (s *session) scheduleAutoRefresh() {
	if !s.autoRefresh || s.closed {
		return
	}
	s.autoTimer = time.AfterFunc(s.autoEvery, func() {
		s.app.update(func() {
			if s.closed || !s.autoRefresh {
				return
			}
			if s.app.cur() != s || s.app.suspended {
				s.scheduleAutoRefresh()
				return
			}
			if s.tool == "browser" {
				if s.valKey != "" {
					if (s.strEditor.Buffer != nil && s.strEditor.Buffer.Modified()) ||
						(s.encodedEditorActive() && s.decodedEditor.Buffer != nil && s.decodedEditor.Buffer.Modified()) {
						s.setStatus("Auto refresh paused: unsaved edits")
					} else if s.app.rowOpen && s.app.rowSessionID == s.id {
						// A row editor is open; keep the table still.
					} else if !s.valLoading && !s.valLoadingMore && !s.valueSaving {
						s.reloadValue()
					}
				}
				s.refreshDatabases()
			}
			s.scheduleAutoRefresh()
		})
	})
}

// reveal opens the branches above a key, so its row shows.
func (s *session) reveal(key string) {
	for n := s.keys.node(key); n != nil && n.parent != nil; {
		n = n.parent
		if n != s.keys.root {
			s.outline.Open.Add(n.path)
		}
	}
}

// selectedKeys returns the chosen keys, sorted, for a batch action.
func (s *session) selectedKeys() []string {
	var keys []string
	for k := range s.sel.All() {
		if n := s.keys.node(k); n != nil && n.isKey {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// renameKey renames one key in Redis and in the browser.
func (s *session) renameKey(old, next string, done func(error)) {
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		err := st.RenameKey(ctx, old, next)
		s.app.update(func() {
			if s.closed {
				return
			}
			if err != nil {
				done(err)
				return
			}
			s.keys.remove(old)
			s.keys.add(next)
			s.reveal(next)
			s.sel.Remove(old)
			s.sel.Add(next)
			if s.valKey == old {
				s.valKey = next
			}
			s.setStatus("Renamed %s to %s", old, next)
			done(nil)
		})
	}()
}

// setTTL expires the keys after seconds, or persists them when it is zero.
func (s *session) setTTL(keys []string, seconds int64, done func(error)) {
	if len(keys) == 0 {
		done(nil)
		return
	}
	ttl := time.Duration(seconds) * time.Second
	st := s.store
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var first error
		for _, key := range keys {
			if err := st.SetTTL(ctx, key, ttl); err != nil && first == nil {
				first = err
				break
			}
		}
		s.app.update(func() {
			if s.closed {
				return
			}
			if first != nil {
				done(first)
				return
			}
			for _, key := range keys {
				if key == s.valKey {
					if seconds <= 0 {
						s.val.TTL = -1
					} else {
						s.val.TTL = ttl
					}
				}
			}
			if len(keys) == 1 {
				s.setStatus("Updated TTL of %s", keys[0])
			} else {
				s.setStatus("Updated TTL of %d keys", len(keys))
			}
			done(nil)
		})
	}()
}

// createKey parses payload and creates a key of type typ.
func (s *session) createKey(name, typ, payload string, seconds int64, done func(error)) {
	v, err := parseNewValue(typ, payload)
	if err != nil {
		done(err)
		return
	}
	ttl := time.Duration(seconds) * time.Second
	st := s.store
	go func() {
		ctx, cancel := st.commandContext()
		defer cancel()
		err := st.CreateKey(ctx, name, typ, v, ttl)
		s.app.update(func() {
			if s.closed {
				return
			}
			if err != nil {
				done(err)
				return
			}
			s.keys.add(name)
			s.reveal(name)
			s.sel.Clear()
			s.sel.Add(name)
			s.clearValue()
			s.setStatus("Created %s", name)
			done(nil)
		})
	}()
}

// flushDB empties the current database and reloads its keyspace.
func (s *session) flushDB(done func(error)) {
	s.keyBusy = true
	st := s.store
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := st.FlushDB(ctx)
		s.app.update(func() {
			s.keyBusy = false
			if s.closed {
				return
			}
			if err != nil {
				done(err)
				return
			}
			s.keys.reset()
			s.sel.Clear()
			s.clearValue()
			s.startScan()
			s.refreshDatabases()
			s.setStatus("Flushed db%d", s.db)
			done(nil)
		})
	}()
}

// parseNewValue turns the new-key form's text into values by type.
func parseNewValue(typ, payload string) (NewKeyValue, error) {
	switch typ {
	case "string":
		return NewKeyValue{String: payload}, nil
	case "json":
		if !json.Valid([]byte(payload)) {
			return NewKeyValue{}, errors.New("value is not valid JSON")
		}
		return NewKeyValue{String: payload}, nil
	case "hash":
		fields := map[string]string{}
		for _, line := range nonEmptyLines(payload) {
			field, value, ok := strings.Cut(line, "=")
			if !ok {
				return NewKeyValue{}, fmt.Errorf("hash line %q needs field=value", line)
			}
			field = strings.TrimSpace(field)
			if field == "" {
				return NewKeyValue{}, fmt.Errorf("hash line %q has an empty field", line)
			}
			fields[field] = strings.TrimSpace(value)
		}
		if len(fields) == 0 {
			return NewKeyValue{}, errors.New("a hash needs at least one field")
		}
		return NewKeyValue{Fields: fields}, nil
	case "list", "set":
		items := nonEmptyLines(payload)
		if len(items) == 0 {
			return NewKeyValue{}, fmt.Errorf("a %s needs at least one element", typ)
		}
		return NewKeyValue{Items: items}, nil
	case "zset":
		scores := map[string]float64{}
		for _, line := range nonEmptyLines(payload) {
			member, score, ok := strings.Cut(line, "=")
			if !ok {
				return NewKeyValue{}, fmt.Errorf("zset line %q needs member=score", line)
			}
			member = strings.TrimSpace(member)
			n, err := strconv.ParseFloat(strings.TrimSpace(score), 64)
			if err != nil || member == "" {
				return NewKeyValue{}, fmt.Errorf("zset line %q has no score", line)
			}
			scores[member] = n
		}
		if len(scores) == 0 {
			return NewKeyValue{}, errors.New("a sorted set needs at least one member")
		}
		return NewKeyValue{Scores: scores}, nil
	default:
		return NewKeyValue{}, fmt.Errorf("type %q is not supported yet", typ)
	}
}

func parseStreamFields(text string) ([][2]string, error) {
	var fields [][2]string
	for _, line := range nonEmptyLines(text) {
		field, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("stream line %q needs field=value", line)
		}
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("stream line %q has an empty field", line)
		}
		fields = append(fields, [2]string{field, strings.TrimSpace(value)})
	}
	if len(fields) == 0 {
		return nil, errors.New("a stream entry needs at least one field")
	}
	return fields, nil
}

func nonEmptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
