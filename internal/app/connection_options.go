package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/redis/go-redis/v9"
)

// These options travel with a saved connection and its active session.
type ConnectionOptions struct {
	Network        string         `json:"network,omitempty"`
	ConnectTimeout int            `json:"connectTimeout,omitempty"`
	CommandTimeout int            `json:"commandTimeout,omitempty"`
	DefaultFilter  string         `json:"defaultFilter,omitempty"`
	Separator      string         `json:"separator,omitempty"`
	KeyView        string         `json:"keyView,omitempty"`
	ScanSize       int            `json:"scanSize,omitempty"`
	DBFilter       string         `json:"dbFilter,omitempty"`
	DBs            []int          `json:"databases,omitempty"`
	DBAliases      map[int]string `json:"dbAliases,omitempty"`
	Color          string         `json:"color,omitempty"`
	Environment    string         `json:"environment,omitempty"`
	ReadOnly       bool           `json:"readOnly,omitempty"`
}

func (o ConnectionOptions) connectionTimeout() time.Duration {
	if o.ConnectTimeout > 0 {
		return time.Duration(o.ConnectTimeout) * time.Second
	}
	return 15 * time.Second
}

func (s *Store) commandContext() (context.Context, context.CancelFunc) {
	d := 15 * time.Second
	if s.cfg.CommandTimeout > 0 {
		d = time.Duration(s.cfg.CommandTimeout) * time.Second
	}
	return context.WithTimeout(context.Background(), d)
}

func (o ConnectionOptions) dbLabel(db int) string {
	label := fmt.Sprintf("db%d", db)
	if alias := o.DBAliases[db]; alias != "" {
		label += " · " + alias
	}
	return label
}

func (o ConnectionOptions) showsDB(db int) bool {
	found := false
	for _, n := range o.DBs {
		if n == db {
			found = true
			break
		}
	}
	switch o.DBFilter {
	case "Show":
		return found
	case "Hide":
		return !found
	default:
		return true
	}
}

func formNumber(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return n
}

func (f *connForm) options() ConnectionOptions {
	o := f.advanced
	o.ConnectTimeout, o.CommandTimeout, o.ScanSize = formNumber(f.connectTimeout), formNumber(f.commandTimeout), formNumber(f.scanSize)
	o.DBs = nil
	for _, v := range splitList(f.dbList) {
		o.DBs = append(o.DBs, formNumber(v))
	}
	o.DBAliases = map[int]string{}
	for _, line := range strings.Split(f.dbAliases, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			o.DBAliases[-1] = line
			continue
		}
		o.DBAliases[formNumber(key)] = strings.TrimSpace(value)
	}
	return o
}

func (f *connForm) fillOptions(o ConnectionOptions) {
	f.advanced = o
	if f.advanced.Environment == "" {
		f.advanced.Environment = "Unspecified"
	}
	if f.advanced.Network == "" {
		f.advanced.Network = "tcp"
	}
	if f.advanced.KeyView == "" {
		f.advanced.KeyView = "Tree"
	}
	if f.advanced.DBFilter == "" {
		f.advanced.DBFilter = "All"
	}
	if f.advanced.Color == "" {
		f.advanced.Color = "Default"
	}
	if f.advanced.Separator == "" {
		f.advanced.Separator = ":"
	}
	f.connectTimeout, f.commandTimeout, f.scanSize = strconv.Itoa(o.ConnectTimeout), strconv.Itoa(o.CommandTimeout), strconv.Itoa(o.ScanSize)
	var dbs, aliases []string
	for _, db := range o.DBs {
		dbs = append(dbs, strconv.Itoa(db))
	}
	for db, name := range o.DBAliases {
		aliases = append(aliases, fmt.Sprintf("%d=%s", db, name))
	}
	sort.Strings(aliases)
	f.dbList, f.dbAliases = strings.Join(dbs, ", "), strings.Join(aliases, "\n")
}

func validateOptions(p Profile) string {
	o := p.ConnectionOptions
	switch o.Environment {
	case "", "Unspecified", "Development", "Testing", "Production":
	default:
		return "Unknown connection environment"
	}
	if o.Network != "" && o.Network != "tcp" && o.Network != "unix" {
		return "Network must be tcp or unix"
	}
	if o.Network == "unix" && (p.mode() != "standalone" || p.Proxy.Type != "" || p.SSH.Enabled) {
		return "Unix sockets require a local standalone connection without a proxy or SSH"
	}
	if o.ConnectTimeout < 0 || o.CommandTimeout < 0 || o.ConnectTimeout > 86400 || o.CommandTimeout > 86400 {
		return "Timeout must be between 0 and 86400 seconds"
	}
	if o.ScanSize < 0 || o.ScanSize > 100000 {
		return "Scan size must be between 0 and 100000"
	}
	if p.DB < 0 {
		return "Database must be a nonnegative number"
	}
	for _, db := range o.DBs {
		if db < 0 {
			return "Database filters need comma-separated nonnegative numbers"
		}
	}
	for db := range o.DBAliases {
		if db < 0 {
			return "Database aliases need one number=name per line"
		}
	}
	return ""
}

func (f *connForm) applyURL(raw string) error {
	o, err := redis.ParseURL(strings.TrimSpace(raw))
	if err != nil {
		return localError("invalid Redis URL")
	} // URLs may contain credentials.
	f.mode, f.addr, f.advanced.Network = "Standalone", o.Addr, o.Network
	f.host, f.port = splitAddress(o.Addr)
	f.user, f.password, f.db = o.Username, o.Password, strconv.Itoa(o.DB)
	f.clearRedis = o.Password == ""
	f.tls, f.tlsInsecure = o.TLSConfig != nil, false
	if o.TLSConfig != nil {
		f.tlsServerName = o.TLSConfig.ServerName
	}
	if o.DialTimeout > 0 {
		f.connectTimeout = strconv.Itoa(int(o.DialTimeout / time.Second))
	}
	if o.ReadTimeout > 0 {
		f.commandTimeout = strconv.Itoa(int(o.ReadTimeout / time.Second))
	}
	return nil
}

func (a *App) viewConnectionOptions(c *ui.Context, pal *palette) {
	ui.Column(c.Key("conn-advanced")).Gap(9).Children(func() {
		for _, field := range []struct {
			label, hint string
			value       *string
		}{
			{"Connect timeout (seconds)", "0 = default", &a.form.connectTimeout},
			{"Command timeout (seconds)", "0 = default", &a.form.commandTimeout},
			{"Default key pattern", "*", &a.form.advanced.DefaultFilter},
			{"Namespace separator", ":", &a.form.advanced.Separator},
			{"Scan size", "0 = preferences", &a.form.scanSize},
		} {
			ui.Field(c.Key(field.label), a.tr(field.label), func() { ui.TextInput(c, field.value).Placeholder(a.tr(field.hint)).Width(360) })
		}
		ui.Field(c, a.tr("Key view"), func() { a.selectOption(c, &a.form.advanced.KeyView, []string{"Tree", "List"}, false).Width(160) })
		ui.Field(c, a.tr("Database visibility"), func() {
			a.selectOption(c, &a.form.advanced.DBFilter, []string{"All", "Show", "Hide"}, false).Width(160)
		})
		if a.form.advanced.DBFilter != "All" {
			ui.Field(c, a.tr("Databases"), func() { ui.TextInput(c, &a.form.dbList).Placeholder("0, 1, 2").Width(360) })
		}
		ui.Column(c.Key("db-aliases-field")).Gap(c.Theme().Space(1.5)).Children(func() {
			focusAliases := false
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				focusAliases = ui.Text(c, a.tr("Database aliases")).FontWeight(500).Clicked()
				helpButton(c, "db-aliases-help", a.tr("Database aliases help"), a.tr("Database aliases help text"), pal).
					Size(16, 16).Radius(8).FontSize(11)
			})
			input := ui.TextArea(c.Key("db-aliases"), &a.form.dbAliases).Label(a.tr("Database aliases")).
				Placeholder(a.tr("0=Cache\n1=Sessions")).Width(360).Height(72)
			if focusAliases {
				input.Focus()
			}
		})
		ui.Field(c, a.tr("Connection color"), func() {
			ui.RadioGroup(c.Key("connection-color"), func() {
				for _, name := range []string{"Default", "Blue", "Green", "Orange", "Red", "Violet"} {
					color := pal.muted
					if name != "Default" {
						color = accentColor(name, pal.dark)
					}
					choice := ui.RadioBase(c.Key(name), &a.form.advanced.Color, name).
						Size(32, 32).Radius(16).Center().Border(2, ui.Transparent).
						Label(a.tr(name)).Tooltip(a.tr(name))
					selected := a.form.advanced.Color == name
					if selected {
						choice.Border(2, color)
					} else if choice.Hovered() {
						choice.Background(pal.rowHover)
					}
					choice.Children(func() {
						ui.Box(c).Size(24, 24).Radius(12).Background(color).Center().Children(func() {
							if selected {
								ui.Box(c).Size(12, 12).Draw(func(p *ui.Painter, r ui.Rect) {
									check := (&ui.Path{}).MoveTo(r.X+1, r.Y+6).LineTo(r.X+4.5, r.Y+9).LineTo(r.X+11, r.Y+2)
									p.StrokePath(check, 1.8, pal.card)
								})
							}
						})
					})
				}
			}).Row().Gap(8).Label(a.tr("Connection color"))
		})
	})
}

func sentinelMasters(cfg ConnConfig) ([]string, error) {
	if len(cfg.SentinelAddrs) == 0 {
		return nil, fmt.Errorf("at least one sentinel address is required")
	}
	var lastErr error
	for _, addr := range cfg.SentinelAddrs {
		probe := cfg
		probe.Mode, probe.Addr, probe.DB = "standalone", addr, 0
		probe.Username, probe.Password = cfg.SentinelUsername, cfg.SentinelPassword
		var st Store
		if lastErr = st.Connect(probe); lastErr != nil {
			continue
		}
		client := redis.NewSentinelClient(&redis.Options{Protocol: 2, Addr: addr, Username: probe.Username, Password: probe.Password, Dialer: st.dial, ContextTimeoutEnabled: true})
		ctx, cancel := st.commandContext()
		masters, err := client.Masters(ctx).Result()
		cancel()
		client.Close()
		st.Close()
		if err != nil {
			lastErr = err
			continue
		}
		names := make([]string, 0, len(masters))
		for _, master := range masters {
			fields, ok := master.([]any)
			if !ok {
				return nil, fmt.Errorf("unexpected sentinel master reply")
			}
			for i := 0; i+1 < len(fields); i += 2 {
				if fmt.Sprint(fields[i]) == "name" {
					names = append(names, fmt.Sprint(fields[i+1]))
					break
				}
			}
		}
		sort.Strings(names)
		return names, nil
	}
	return nil, lastErr
}

func (a *App) discoverMasters() {
	if a.form.discovering {
		return
	}
	p := a.form.profile(a.selProfile)
	secrets, err := a.profileSecrets(p)
	if err != nil && !a.form.hasSecretInput() {
		a.connErr = errorMessage(err)
		return
	}
	cfg := p.config(a.form.mergeSecrets(secrets))
	input, profile := a.form.sentinelAddrs, a.selProfile
	a.form.discovering = true
	a.masterDiscoveryID++
	generation := a.masterDiscoveryID
	go func() {
		names, err := sentinelMasters(cfg)
		a.update(func() {
			if generation != a.masterDiscoveryID {
				return
			}
			a.form.discovering = false
			if a.selProfile != profile || a.form.sentinelAddrs != input || a.form.mode != "Sentinel" {
				return
			}
			if err != nil {
				a.connErr = errorMessage(err)
				return
			}
			a.connErr, a.form.masters = message{}, names
			if a.form.masterName == "" && len(names) > 0 {
				a.form.masterName = names[0]
			}
		})
	}()
}
