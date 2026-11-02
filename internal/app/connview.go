package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	localvault "github.com/cuikaipeng/kivrix/internal/secrets"
)

// connForm is the connection editor's state, filled from a Profile.
type connForm struct {
	masters                                                          []string
	discovering                                                      bool
	advanced                                                         ConnectionOptions
	showAdvanced                                                     bool
	url, connectTimeout, commandTimeout, scanSize, dbList, dbAliases string
	order                                                            int
	name, group                                                      string
	addr, host, port                                                 string
	user, password                                                   string
	mode                                                             string
	db                                                               string
	sentinelAddrs, masterName                                        string
	sentinelUser, sentinelPass                                       string
	clusterAddrs                                                     string

	tls                    bool
	tlsCA, tlsCert, tlsKey string
	tlsServerName          string
	tlsInsecure            bool

	proxy                string
	proxyAddr, proxyUser string
	proxyPassword        string

	ssh                        bool
	sshAddr, sshUser           string
	sshPassword, sshPassphrase string
	sshKey                     string
	sshAgent                   bool
	sshInsecure                bool

	clearRedis bool
}

func (f *connForm) reset() {
	*f = connForm{host: "127.0.0.1", port: "6379", db: "0", mode: "Standalone", proxy: "None"}
	f.fillOptions(ConnectionOptions{})
}

// profile builds a Profile from the form. The caller stores passwords in
// the encrypted local vault, never in the profile itself.
func (f *connForm) profile(id string) Profile {
	db := formNumber(f.db)
	mode := "standalone"
	switch f.mode {
	case "Sentinel", "sentinel":
		mode = "sentinel"
	case "Cluster", "cluster":
		mode = "cluster"
	}
	proxy := strings.ToLower(strings.TrimSpace(f.proxy))
	if proxy == "none" {
		proxy = ""
	}
	p := Profile{
		ConnectionOptions: f.options(),
		Order:             f.order,
		ID:                id,
		Name:              strings.TrimSpace(f.name),
		Group:             strings.TrimSpace(f.group),
		Mode:              mode,
		Addr:              f.address(),
		Username:          strings.TrimSpace(f.user),
		DB:                db,
		UseTLS:            f.tls,
		TLS:               TLSConfig{CA: strings.TrimSpace(f.tlsCA), Cert: strings.TrimSpace(f.tlsCert), Key: strings.TrimSpace(f.tlsKey), ServerName: strings.TrimSpace(f.tlsServerName), Insecure: f.tlsInsecure},
		SentinelAddrs:     splitList(f.sentinelAddrs),
		MasterName:        strings.TrimSpace(f.masterName),
		SentinelUser:      strings.TrimSpace(f.sentinelUser),
		ClusterAddrs:      splitList(f.clusterAddrs),
		Proxy:             ProxyConfig{Type: proxy, Addr: strings.TrimSpace(f.proxyAddr), Username: strings.TrimSpace(f.proxyUser)},
		SSH: SSHConfig{
			Enabled:  f.ssh,
			Addr:     strings.TrimSpace(f.sshAddr),
			User:     strings.TrimSpace(f.sshUser),
			KeyPath:  strings.TrimSpace(f.sshKey),
			Agent:    f.sshAgent,
			Insecure: f.sshInsecure,
		},
	}
	if mode == "cluster" {
		p.DB = 0
	}
	return p
}

func (f *connForm) fill(p Profile) {
	f.fillOptions(p.ConnectionOptions)
	f.order, f.url = p.Order, ""
	f.masters = nil
	f.name, f.group, f.user = p.Name, p.Group, p.Username
	f.addr = p.Addr
	f.host, f.port = splitAddress(p.Addr)
	f.mode = titleMode(p.mode())
	f.db = strconv.Itoa(p.DB)
	f.sentinelAddrs = strings.Join(p.SentinelAddrs, ", ")
	f.masterName, f.sentinelUser = p.MasterName, p.SentinelUser
	f.clusterAddrs = strings.Join(p.ClusterAddrs, ", ")
	f.tls = p.UseTLS
	f.tlsCA, f.tlsCert, f.tlsKey = p.TLS.CA, p.TLS.Cert, p.TLS.Key
	f.tlsServerName, f.tlsInsecure = p.TLS.ServerName, p.TLS.Insecure
	f.proxy = "None"
	switch p.Proxy.Type {
	case "http":
		f.proxy = "HTTP"
	case "socks5":
		f.proxy = "SOCKS5"
	}
	f.proxyAddr, f.proxyUser = p.Proxy.Addr, p.Proxy.Username
	f.ssh = p.SSH.Enabled
	f.sshAddr, f.sshUser, f.sshKey = p.SSH.Addr, p.SSH.User, p.SSH.KeyPath
	f.sshAgent, f.sshInsecure = p.SSH.Agent, p.SSH.Insecure
	f.password, f.sshPassword, f.sshPassphrase = "", "", ""
	f.sentinelPass, f.proxyPassword = "", ""
	f.clearRedis = false
}

// address joins the host and port fields, falling back to the raw address
// for forms built before the fields were split (and imported profiles).
func (f *connForm) address() string {
	if f.advanced.Network == "unix" {
		return strings.TrimSpace(f.addr)
	}
	if strings.TrimSpace(f.host) == "" && strings.TrimSpace(f.port) == "" {
		return strings.TrimSpace(f.addr)
	}
	return joinAddress(f.host, f.port)
}

func splitAddress(addr string) (string, string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "6379"
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return host, port
	}
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		return strings.Trim(addr, "[]"), "6379"
	}
	if strings.Count(addr, ":") == 1 {
		host, port, _ := strings.Cut(addr, ":")
		return host, port
	}
	return addr, "6379"
}

func joinAddress(host, port string) string {
	host, port = strings.TrimSpace(host), strings.TrimSpace(port)
	if host == "" {
		return ""
	}
	if port == "" {
		port = "6379"
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]")
	}
	return net.JoinHostPort(host, port)
}

func (f *connForm) hasSecretInput() bool {
	return f.password != "" || f.sshPassword != "" || f.sshPassphrase != "" ||
		f.sentinelPass != "" || f.proxyPassword != "" || f.clearRedis
}

// mergeSecrets keeps saved values unless the form carries a replacement.
func (f *connForm) mergeSecrets(base SecretSet) SecretSet {
	if f.password != "" {
		base.Redis = f.password
	}
	if f.clearRedis {
		base.Redis = ""
	}
	if f.sshPassword != "" {
		base.SSH = f.sshPassword
	}
	if f.sshPassphrase != "" {
		base.SSHPassphrase = f.sshPassphrase
	}
	if f.sentinelPass != "" {
		base.Sentinel = f.sentinelPass
	}
	if f.proxyPassword != "" {
		base.Proxy = f.proxyPassword
	}
	return base
}

func (a *App) fillConnForm(p Profile) {
	a.masterDiscoveryID++
	a.form.discovering = false
	a.form.fill(p)
	a.form.showAdvanced = p.Group != "" || p.SSH.Enabled || p.Proxy.Type != "" && p.Proxy.Type != "none" || p.Network == "unix"
	a.connTestResult = message{}
	a.connTestErr = false
}

func titleMode(mode string) string {
	switch mode {
	case "sentinel":
		return "Sentinel"
	case "cluster":
		return "Cluster"
	default:
		return "Standalone"
	}
}

// validateProfile checks the parts that differ by connection mode.
func validateProfile(p Profile) string {
	if err := validateOptions(p); err != "" {
		return err
	}
	switch p.mode() {
	case "sentinel":
		if len(p.SentinelAddrs) == 0 {
			return "At least one sentinel address is required"
		}
		if p.MasterName == "" {
			return "A master name is required"
		}
	case "cluster":
		if len(p.ClusterAddrs) == 0 {
			return "At least one cluster node address is required"
		}
	default:
		if p.Addr == "" {
			return "Address is required"
		}
	}
	if p.UseTLS && (p.TLS.Cert == "") != (p.TLS.Key == "") {
		return "TLS needs both a client certificate and key"
	}
	if p.Proxy.Type != "" && p.Proxy.Addr == "" {
		return "A proxy address is required"
	}
	if p.SSH.Enabled {
		if p.SSH.Addr == "" {
			return "An SSH address is required"
		}
	}
	return ""
}

// secretEntries lists the saved values that belong to a profile.
func secretEntries(id string, p Profile, s SecretSet) []struct{ name, value string } {
	out := []struct{ name, value string }{{"redis", s.Redis}}
	if p.SSH.Enabled {
		out = append(out, struct{ name, value string }{"ssh", s.SSH})
		out = append(out, struct{ name, value string }{"ssh-passphrase", s.SSHPassphrase})
	}
	if p.mode() == "sentinel" {
		out = append(out, struct{ name, value string }{"sentinel", s.Sentinel})
	}
	if p.Proxy.Type != "" {
		out = append(out, struct{ name, value string }{"proxy", s.Proxy})
	}
	return out
}

func (a *App) storeSecrets(id string, p Profile, s SecretSet) error {
	entries := secretEntries(id, p, s)
	wanted := map[string]bool{}
	for _, e := range entries {
		wanted[e.name] = true
		if err := saveSecret("profile/"+id+"/"+e.name, e.value); err != nil {
			return fmt.Errorf("%s secret: %w", e.name, err)
		}
	}
	// A profile that no longer uses SSH, a proxy or sentinel must not keep
	// the old secret around.
	for _, kind := range []string{"redis", "ssh", "ssh-passphrase", "sentinel", "proxy"} {
		if wanted[kind] {
			continue
		}
		if err := secretDelete("profile/" + id + "/" + kind); err != nil {
			return fmt.Errorf("%s secret: %w", kind, err)
		}
	}
	if a.secretCache == nil {
		a.secretCache = map[string]SecretSet{}
	}
	a.secretCache[id] = s
	return nil
}

// Secrets are variables so tests can replace the local vault.
var (
	secretGet    = func(string) ([]byte, error) { return nil, mygo.ErrSecretNotFound }
	secretSet    = func(string, []byte) error { return errors.New("password store is not available") }
	secretDelete = func(string) error { return nil }
)

// initSecretStore opens the encrypted local vault.
func (a *App) initSecretStore(dir string) {
	s, err := localvault.Open(dir)
	if err != nil {
		a.setStatusErr(localError("local secrets: %s", err))
		return
	}
	a.secretCache = map[string]SecretSet{}
	secretGet, secretSet, secretDelete = s.Get, s.Set, s.Delete
}

// profileSecrets reads the vault entries a profile needs, once per
// session.
func (a *App) profileSecrets(p Profile) (SecretSet, error) {
	if s, ok := a.secretCache[p.ID]; ok {
		return s, nil
	}
	var out SecretSet
	load := func(kind string, dst *string) error {
		v, err := secretGet("profile/" + p.ID + "/" + kind)
		if errors.Is(err, mygo.ErrSecretNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		*dst = string(v)
		return nil
	}
	if err := load("redis", &out.Redis); err != nil {
		return out, err
	}
	if p.SSH.Enabled {
		if err := load("ssh", &out.SSH); err != nil {
			return out, err
		}
		if err := load("ssh-passphrase", &out.SSHPassphrase); err != nil {
			return out, err
		}
	}
	if p.mode() == "sentinel" {
		if err := load("sentinel", &out.Sentinel); err != nil {
			return out, err
		}
	}
	if p.Proxy.Type != "" {
		if err := load("proxy", &out.Proxy); err != nil {
			return out, err
		}
	}
	if a.secretCache == nil {
		a.secretCache = map[string]SecretSet{}
	}
	a.secretCache[p.ID] = out
	return out, nil
}

// loadSecret reads a saved entry, treating any error as empty.
func loadSecret(key string) string {
	v, err := secretGet(key)
	if err != nil {
		return ""
	}
	return string(v)
}

// saveSecret replaces a profile's secret, removing it when value is empty.
func saveSecret(key, value string) error {
	if value == "" {
		return secretDelete(key)
	}
	return secretSet(key, []byte(value))
}

// viewConnSidebar is the disconnected sidebar: saved connections, with
// the selected one edited in the card beside it.
func (a *App) viewConnSidebar(c *ui.Context, pal *palette) {
	a.syncConnSelection()
	var more ui.Element
	ui.Row(c).Height(cardHeaderH).Shrink(0).Padding(0, 12).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.tr("Connections")).FontSize(13).FontWeight(600).TextColor(pal.text).Grow(1)
		if len(a.profiles) > 0 {
			ui.Textf(c, "%d", len(a.profiles)).FontSize(11).FontWeight(600).TextColor(pal.faint)
		}
		more = iconButton(c, "conn-more", iconMore, a.tr("Connection actions"), pal, func() {
			a.connMenuOpen = !a.connMenuOpen
		})
		iconButton(c, "conn-new", iconPlus, a.tr("New Connection"), pal, a.newConnection)
	})
	if more.Valid() && a.connMenuOpen {
		ui.Popover(c, more, &a.connMenuOpen, func() {
			a.connMenu(c, pal)
		})
	}
	if len(a.profiles) == 0 && len(a.prefs.Groups) == 0 {
		ui.Column(c).Grow(1).Center().Padding(20).Children(func() {
			ui.Box(c).Size(40, 40).Radius(10).Background(pal.chip).Center().Children(func() {
				ui.Icon(c, iconServer).Size(19, 19).TextColor(pal.muted)
			})
			ui.Text(c, a.tr("No connections yet")).FontSize(12.5).FontWeight(600).TextColor(pal.text).Margin(12, 0, 0, 0)
			ui.Text(c, a.tr("Add a server to start browsing.")).FontSize(11).TextColor(pal.faint).
				TextAlign(ui.Center).MaxWidth(200).LineHeight(1.35).Margin(4, 0, 0, 0)
			if textButton(c, "conn-empty-new", a.tr("New Connection"), true, false, pal).Margin(14, 0, 0, 0).Clicked() {
				a.newConnection()
			}
			if textButton(c, "conn-empty-import", a.tr("Import connections…"), false, false, pal).Margin(6, 0, 0, 0).Clicked() {
				a.importProfiles()
			}
		})
		return
	}
	ui.Column(c).Padding(0, 10, 8, 10).Shrink(0).Children(func() {
		a.searchField(c, pal, &a.connQuery, "Filter connections")
	})
	groups := a.connGroups()
	if len(groups) == 0 {
		ui.Column(c).Grow(1).Center().Padding(20).Children(func() {
			ui.Text(c, a.tr("No matching connections")).FontSize(12).FontWeight(600).TextColor(pal.text)
			ui.Text(c, a.tr("Try a different name, address or group.")).FontSize(11).TextColor(pal.faint).
				TextAlign(ui.Center).MaxWidth(200).Margin(4, 0, 0, 0)
			if textButton(c, "conn-clear-filter", a.tr("Clear filter"), false, false, pal).Margin(12, 0, 0, 0).Clicked() {
				a.connQuery = ""
			}
		})
		return
	}
	ui.Scroll(c).Grow(1).TrackScroll(&a.connScroll).Children(func() {
		ui.Column(c).Padding(6).Gap(2).Children(func() {
			for _, g := range groups {
				a.connGroupRow(c, pal, g.name, len(g.profiles))
				if a.connCollapsed[g.name] {
					continue
				}
				for _, i := range g.profiles {
					a.connProfileRow(c, pal, i)
				}
			}
		})
	})
}

// connGroup is a group of saved connections, with their profile indexes.
type connGroup struct {
	name     string
	profiles []int
}

// connGroups filters and groups the profiles. An empty group name is the
// ungrouped section and sorts last.
func (a *App) connGroups() []connGroup {
	q := strings.ToLower(strings.TrimSpace(a.connQuery))
	byName := map[string][]int{}
	var names []string
	order := map[string]int{}
	for i, name := range a.groupNames() {
		order[name] = i
		if q == "" || strings.Contains(strings.ToLower(name), q) {
			names = append(names, name)
			byName[name] = nil
		}
	}
	for i, p := range a.profiles {
		if q != "" {
			hay := strings.ToLower(p.label() + " " + p.summary() + " " + p.Group)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		if _, ok := byName[p.Group]; !ok {
			names = append(names, p.Group)
		}
		byName[p.Group] = append(byName[p.Group], i)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "" {
			return false
		}
		if names[j] == "" {
			return true
		}
		return order[names[i]] < order[names[j]]
	})
	out := make([]connGroup, 0, len(names))
	for _, name := range names {
		out = append(out, connGroup{name: name, profiles: byName[name]})
	}
	return out
}

func groupLabel(group string) string {
	if group == "" {
		return "Ungrouped"
	}
	return group
}

// syncConnSelection keeps the form in step with connSel, including tests
// and screens that select a profile without a pointer-bound list.
func (a *App) syncConnSelection() {
	if a.connSel >= 0 && a.connSel < len(a.profiles) {
		p := a.profiles[a.connSel]
		if a.selProfile != p.ID {
			a.selProfile = p.ID
			a.fillConnForm(p)
			a.connErr = message{}
		}
		return
	}
	if a.selProfile == "" {
		return
	}
	for i, p := range a.profiles {
		if p.ID == a.selProfile {
			a.connSel = i
			return
		}
	}
	a.selProfile = ""
	a.connSel = -1
	a.form.reset()
}

func (a *App) connGroupRow(c *ui.Context, pal *palette, group string, count int) {
	open := !a.connCollapsed[group]
	r := ui.Row(c.Key("conn-group-"+group)).Height(28).Padding(6, 7).Gap(4).Radius(6).
		AlignItems(ui.Center).FocusRing(false)
	if r.Hovered() {
		r.Background(pal.rowHover)
	}
	r.Children(func() {
		icon := iconChevronRight
		if open {
			icon = iconChevronDown
		}
		ui.Icon(c, icon).Size(12, 12).TextColor(pal.faint)
		ui.Icon(c, iconFolder).Size(13, 13).TextColor(pal.muted)
		ui.Text(c, a.tr(groupLabel(group))).FontSize(11.5).FontWeight(600).TextColor(pal.muted).
			Grow(1).SingleLine().Ellipsis("…")
		ui.Textf(c, "%d", count).FontSize(11).FontWeight(600).TextColor(pal.faint).Padding(1, 4).
			Radius(4).Background(pal.chip)
	})
	if r.Clicked() {
		a.connCollapsed[group] = !a.connCollapsed[group]
	}
	r.ContextMenu(func(m *ui.Menu) {
		if m.Item(a.tr("Move up")).Chosen() {
			a.moveGroup(group, -1)
		}
		if m.Item(a.tr("Move down")).Chosen() {
			a.moveGroup(group, 1)
		}
		if m.Item(a.tr("Rename group…")).Chosen() {
			a.openGroupRename(group)
		}
		if m.Item(a.tr("Delete group")).Chosen() {
			a.deleteGroup(group)
		}
	})
}

func (a *App) connProfileRow(c *ui.Context, pal *palette, index int) {
	p := a.profiles[index]
	selected := a.selProfile == p.ID
	r := ui.Row(c.Key("conn-" + p.ID)).Height(54).Padding(8).Gap(9).Radius(8).
		AlignItems(ui.Center).FocusRing(false)
	switch {
	case selected:
		chosen(r, pal)
	case r.Hovered():
		r.Background(pal.rowHover)
	}
	r.Children(func() {
		ui.Box(c).Size(28, 28).Radius(8).Background(pal.chip).Center().Children(func() {
			color := pal.muted
			if p.Color != "" && p.Color != "Default" {
				color = accentColor(p.Color, pal.dark)
			}
			ui.Icon(c, iconDatabase).Size(14, 14).TextColor(color)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Text(c, p.label()).FontSize(12.5).FontWeight(550).TextColor(pal.text).
					Grow(1).SingleLine().Ellipsis("…")
				switch p.mode() {
				case "sentinel":
					smallBadge(c, "SENT", pal)
				case "cluster":
					smallBadge(c, "CLUSTER", pal)
				}
				if p.Proxy.Type != "" {
					smallBadge(c, "PROXY", pal)
				}
				if p.UseTLS {
					smallBadge(c, "TLS", pal)
				}
				if p.SSH.Enabled {
					smallBadge(c, "SSH", pal)
				}
			})
			ui.Row(c).Gap(5).Children(func() { a.connectionBadges(c, pal, p.ConnectionOptions) })
			summary := p.summary()
			switch p.mode() {
			case "sentinel":
				summary = a.tr("sentinel") + " · " + p.MasterName
			case "cluster":
				summary = a.tr("cluster") + " · " + strings.Join(p.ClusterAddrs, ", ")
			}
			ui.Text(c, summary).FontSize(11).TextColor(pal.faint).SingleLine().Ellipsis("…")
		})
	})
	if r.Clicked() {
		a.selProfile = p.ID
		a.connSel = index
		a.fillConnForm(p)
		a.connErr = message{}
		a.connMenuOpen = false
	}
	if r.DoubleClicked() {
		a.selProfile = p.ID
		a.connSel = index
		a.fillConnForm(p)
		a.connErr = message{}
		a.connMenuOpen = false
		a.connectSelected()
	}
	r.ContextMenu(func(m *ui.Menu) {
		if m.Item(a.tr("Move up")).Chosen() {
			a.moveProfile(p.ID, -1)
		}
		if m.Item(a.tr("Move down")).Chosen() {
			a.moveProfile(p.ID, 1)
		}
		if m.Item(a.tr("Duplicate")).Chosen() {
			a.duplicateProfile(p.ID)
		}
		if m.Item(a.tr("Delete")).Chosen() {
			a.askDeleteProfile(p.ID)
		}
	})
}

// connMenu offers profile import/export and disconnecting every session.
func (a *App) connMenu(c *ui.Context, pal *palette) {
	ui.Column(c).Width(220).Padding(4).Children(func() {
		menuItem(c, pal, a.tr("New group"), iconFolder, false, func() {
			a.connMenuOpen = false
			a.openGroupRename("")
			a.groupCreating = true
		})
		menuItem(c, pal, a.tr("Import connections…"), iconDownload, false, func() {
			a.connMenuOpen = false
			a.importProfiles()
		})
		menuItem(c, pal, a.tr("Export connections…"), iconUpload, false, func() {
			a.connMenuOpen = false
			a.exportProfiles()
		})
		ui.Box(c).Height(1).Background(pal.divider).Margin(4, 4)
		menuItem(c, pal, a.tr("Check for updates"), iconRefresh, false, func() {
			a.connMenuOpen = false
			a.checkUpdates()
		})
		if len(a.sessions) > 0 {
			ui.Box(c).Height(1).Background(pal.divider).Margin(4, 4)
			menuItem(c, pal, a.tr("Disconnect all"), iconUnplug, true, func() {
				a.connMenuOpen = false
				a.disconnectAll()
			})
		}
	})
}

// viewConnCard is the card beside the connection list: the form for the
// selected profile or a new one.
func (a *App) viewConnCard(c *ui.Context, pal *palette) {
	title, detail := a.tr("New Connection"), a.tr("Saved passwords stay in the encrypted local vault.")
	if a.selProfile != "" {
		title, detail = a.tr("Edit Connection"), a.tr("Changes apply the next time you connect.")
	}
	cardTitle(c, pal, title, detail, func() {
		if a.connConnecting {
			ui.Text(c, a.tr("Connecting…")).FontSize(11).TextColor(pal.warning)
		}
	})
	a.viewConnForm(c, pal)
	a.viewConnectionActions(c, pal)
}

// newConnection clears the editor for a profile that does not exist yet.
func (a *App) newConnection() {
	a.masterDiscoveryID++
	a.selProfile = ""
	a.form.reset()
	a.connErr = message{}
	a.connTestResult = message{}
	a.connTestErr = false
	a.connSel = -1
}

func (a *App) viewConnForm(c *ui.Context, pal *palette) {
	ui.Scroll(c.Key("conn-form")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(20).Gap(10).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &a.form.url).Password().Placeholder("redis:// / rediss:// / unix://").Width(270).Label(a.tr("Connection URL"))
				if textButton(c, "conn-url", a.tr("Use URL"), false, false, pal).Clicked() {
					if err := a.form.applyURL(a.form.url); err != nil {
						a.connErr = errorMessage(err)
					} else {
						a.form.url, a.connErr = "", message{}
					}
				}
				helpButton(c, "conn-url-help", a.tr("Connection URL help"), a.tr("Connection URL help text"), pal)
			})
			ui.Field(c, a.tr("Name"), func() {
				ui.TextInput(c, &a.form.name).Placeholder(a.tr("My server")).Width(360)
			})
			ui.Field(c.Key("connection-group"), a.tr("Group"), func() {
				ui.TextInput(c, &a.form.group).Placeholder(a.tr("Optional; creates a group")).Width(360)
			})
			ui.Row(c).Gap(10).AlignItems(ui.End).Children(func() {
				ui.Field(c, a.tr("Mode"), func() {
					a.selectOption(c, &a.form.mode, []string{"Standalone", "Sentinel", "Cluster"}, false).Width(150)
				})
				if a.form.mode != "Cluster" {
					ui.Field(c, a.tr("DB"), func() {
						ui.TextInput(c, &a.form.db).Placeholder("0").Width(80)
					})
				}
			})

			switch a.form.mode {
			case "Sentinel":
				ui.Field(c, a.tr("Sentinel Addresses"), func() {
					ui.TextInput(c, &a.form.sentinelAddrs).Placeholder("host:26379, host:26380").Width(360)
				})
				ui.Field(c, a.tr("Master Name"), func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.TextInput(c, &a.form.masterName).Placeholder("mymaster").Width(210)
						label := "Discover masters"
						if a.form.discovering {
							label = "Discovering…"
						}
						if textButton(c, "sentinel-discover", a.tr(label), false, false, pal).Disabled(a.form.discovering).Clicked() {
							a.discoverMasters()
						}
					})
					if len(a.form.masters) > 0 {
						a.selectOption(c, &a.form.masterName, a.form.masters, false).Width(360)
					}
				})
				ui.Row(c).Gap(10).Children(func() {
					ui.Field(c, a.tr("Sentinel Username"), func() {
						ui.TextInput(c, &a.form.sentinelUser).Placeholder(a.tr("optional")).Width(175)
					})
					ui.Field(c, a.tr("Sentinel Password"), func() {
						ui.TextInput(c, &a.form.sentinelPass).Placeholder(a.tr("optional")).Password().Width(175)
					})
				})
			case "Cluster":
				ui.Field(c, a.tr("Cluster Addresses"), func() {
					ui.TextInput(c, &a.form.clusterAddrs).Placeholder("host:7000, host:7001, host:7002").Width(360)
				})
			default:
				if a.form.advanced.Network == "unix" {
					ui.Field(c, a.tr("Socket path"), func() { ui.TextInput(c, &a.form.addr).Placeholder("/tmp/redis.sock").Width(360) })
				} else {
					ui.Row(c).Gap(10).Children(func() {
						ui.Field(c, a.tr("Host"), func() {
							ui.TextInput(c, &a.form.host).Placeholder("127.0.0.1").Width(250)
						})
						ui.Field(c, a.tr("Port"), func() {
							ui.TextInput(c, &a.form.port).Placeholder("6379").Width(90)
						})
					})
				}
			}

			ui.Row(c).Gap(10).Children(func() {
				ui.Field(c, a.tr("Username"), func() {
					ui.TextInput(c, &a.form.user).Placeholder(a.tr("optional")).Width(175)
				})
				ui.Field(c, a.tr("Password"), func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						in := ui.TextInput(c, &a.form.password).Placeholder(a.tr("saved locally")).Password().Width(125)
						if in.Changed() {
							a.form.clearRedis = false
						}
						if a.selProfile != "" {
							if textButton(c, "conn-clear-password", a.tr("Clear"), false, true, pal).Clicked() {
								a.form.password = ""
								a.form.clearRedis = true
							}
						}
					})
				})
			})
			ui.Text(c, a.tr("Saved passwords are read only when connecting, testing or saving.")).
				FontSize(11).TextColor(pal.faint).LineHeight(1.3)

			ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
				ui.Field(c, a.tr("Environment"), func() {
					a.selectOption(c, &a.form.advanced.Environment, []string{"Unspecified", "Development", "Testing", "Production"}, false).Width(160)
				})
				ui.Checkbox(c, &a.form.advanced.ReadOnly, a.tr("Read-only connection"))
			})
			ui.Checkbox(c, &a.form.tls, a.tr("TLS"))
			if a.form.tls {
				ui.Column(c).Gap(8).PaddingStart(20).Children(func() {
					ui.Field(c, a.tr("CA Bundle"), func() {
						ui.TextInput(c, &a.form.tlsCA).Placeholder(a.tr("~/.redis/ca.pem (optional)")).Width(360)
					})
					ui.Row(c).Gap(10).Children(func() {
						ui.Field(c, a.tr("Client Certificate"), func() {
							ui.TextInput(c, &a.form.tlsCert).Placeholder("cert.pem").Width(175)
						})
						ui.Field(c, a.tr("Client Key"), func() {
							ui.TextInput(c, &a.form.tlsKey).Placeholder("key.pem").Width(175)
						})
					})
					ui.Field(c, a.tr("Server Name (SNI)"), func() {
						ui.TextInput(c, &a.form.tlsServerName).Placeholder(a.tr("verify against a different name")).Width(360)
					})
					ui.Checkbox(c, &a.form.tlsInsecure, a.tr("Skip TLS verification"))
					ui.Text(c, a.tr("Without a CA, the system roots verify the server certificate.")).FontSize(11).TextColor(pal.faint)
				})
			}

			ui.Checkbox(c, &a.form.showAdvanced, a.tr("Advanced settings"))
			if a.form.showAdvanced {
				ui.Column(c.Key("connection-details")).Gap(10).Children(func() {
					ui.Field(c, a.tr("Network"), func() { a.selectOption(c, &a.form.advanced.Network, []string{"tcp", "unix"}, false).Width(160) })
					ui.Row(c).Gap(10).AlignItems(ui.End).Children(func() {
						ui.Field(c, a.tr("Proxy"), func() {
							a.selectOption(c, &a.form.proxy, []string{"None", "HTTP", "SOCKS5"}, false).Width(130)
						})
						if a.form.proxy != "None" {
							ui.Field(c, a.tr("Proxy Address"), func() {
								ui.TextInput(c, &a.form.proxyAddr).Placeholder("host:8080").Width(220)
							})
						}
					})
					if a.form.proxy != "None" {
						ui.Row(c).Gap(10).PaddingStart(20).Children(func() {
							ui.Field(c, a.tr("Proxy Username"), func() {
								ui.TextInput(c, &a.form.proxyUser).Placeholder(a.tr("optional")).Width(175)
							})
							ui.Field(c, a.tr("Proxy Password"), func() {
								ui.TextInput(c, &a.form.proxyPassword).Placeholder(a.tr("optional")).Password().Width(175)
							})
						})
					}

					ui.Checkbox(c, &a.form.ssh, a.tr("SSH tunnel"))
					if a.form.ssh {
						ui.Column(c).Gap(8).PaddingStart(20).Children(func() {
							ui.Row(c).Gap(10).Children(func() {
								ui.Field(c, a.tr("SSH Address"), func() {
									ui.TextInput(c, &a.form.sshAddr).Placeholder("host:22").Width(190)
								})
								ui.Field(c, a.tr("SSH User"), func() {
									ui.TextInput(c, &a.form.sshUser).Placeholder(a.tr("user")).Width(150)
								})
							})
							ui.Field(c, a.tr("SSH Password"), func() {
								ui.TextInput(c, &a.form.sshPassword).Placeholder(a.tr("password, or use a key/agent")).Password().Width(360)
							})
							ui.Field(c, a.tr("SSH Key Path"), func() {
								ui.TextInput(c, &a.form.sshKey).Placeholder("~/.ssh/id_ed25519").Width(360)
							})
							ui.Field(c, a.tr("Key Passphrase"), func() {
								ui.TextInput(c, &a.form.sshPassphrase).Placeholder(a.tr("optional")).Password().Width(360)
							})
							ui.Checkbox(c, &a.form.sshAgent, a.tr("Use SSH agent (SSH_AUTH_SOCK)"))
							ui.Checkbox(c, &a.form.sshInsecure, a.tr("Skip host key check"))
							ui.Text(c, a.tr("Host keys are checked against ~/.ssh/known_hosts.")).FontSize(11).TextColor(pal.faint)
						})
					}

					a.viewConnectionOptions(c, pal)
				})
			}

		})
	})
}

func (a *App) viewConnectionActions(c *ui.Context, pal *palette) {
	ui.Column(c.Key("connection-actions")).Shrink(0).Padding(12, 20).Gap(8).Children(func() {
		if a.connErr.key != "" {
			ui.Row(c).Padding(8, 10).Radius(8).Gap(7).AlignItems(ui.Start).
				Background(pal.danger.Alpha(0.08)).Children(func() {
				ui.Icon(c, iconAlert).Size(14, 14).TextColor(pal.danger)
				ui.Text(c, a.connErr.text(a)).FontSize(11.5).TextColor(pal.danger).Grow(1)
			})
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			connectLabel := a.tr("Connect")
			if a.connConnecting {
				connectLabel = a.tr("Connecting…")
			}
			if textButton(c, "conn-connect", connectLabel, true, false, pal).
				Disabled(a.connConnecting).Clicked() {
				a.connectSelected()
			}
			if textButton(c, "conn-save", a.tr("Save"), false, false, pal).
				Disabled(a.connConnecting).Clicked() {
				a.saveProfile()
			}
			testLabel := a.tr("Test connection")
			if a.connTesting {
				testLabel = a.tr("Testing…")
			}
			if textButton(c, "conn-test", testLabel, false, false, pal).
				Disabled(a.connConnecting || a.connTesting).Clicked() {
				a.testConnection()
			}
			if a.selProfile != "" {
				if textButton(c, "conn-delete", a.tr("Delete"), false, true, pal).
					Disabled(a.connConnecting).Clicked() {
					a.deleteProfile(a.selProfile)
				}
			}
		})
		if a.connTestResult.key != "" {
			color := pal.success
			if a.connTestErr {
				color = pal.danger
			}
			ui.Text(c, a.connTestResult.text(a)).FontSize(11).TextColor(color).LineHeight(1.3)
		}
	})
}

// saveProfile upserts the form's profile and stores its passwords in the
// encrypted local vault.
func (a *App) saveProfile() {
	p := a.form.profile(a.selProfile)
	if msg := validateProfile(p); msg != "" {
		a.connErr = message{key: msg}
		return
	}
	if p.ID == "" {
		p.ID = newProfileID()
	}
	a.connErr = message{}
	if err := a.upsertProfile(p); err != nil {
		a.connErr = errorMessage(err)
		return
	}
	a.selProfile = p.ID
	secrets, err := a.profileSecrets(p)
	if err != nil {
		if !a.form.hasSecretInput() {
			a.connErr = msg("Could not read the saved password: %s", err)
			return
		}
		secrets = SecretSet{}
	}
	secrets = a.form.mergeSecrets(secrets)
	if err := a.storeSecrets(p.ID, p, secrets); err != nil {
		a.connErr = msg("Profile saved, but a secret could not be stored: %s", err)
		a.setStatusErr(err)
		return
	}
	a.connErr = message{}
	a.setStatus("Saved %s", p.label())
}

// upsertProfile saves p to the list and to disk.
func (a *App) upsertProfile(p Profile) error {
	for i := range a.profiles {
		if a.profiles[i].ID == p.ID {
			a.profiles[i] = p
			goto found
		}
	}
	a.profiles = append(a.profiles, p)
found:
	a.prefs.Groups = a.groupNames()
	a.savePrefs()
	sortProfiles(a.profiles)
	if a.profilesPath != "" {
		return saveProfiles(a.profilesPath, a.profiles)
	}
	return nil
}

func (a *App) deleteProfile(id string) {
	a.protectEdits(a.sessionByID(id), func() { a.deleteProfileNow(id) })
}

func (a *App) deleteProfileNow(id string) {
	if i := a.sessionIndex(id); i >= 0 {
		a.closeSessionNow(i)
	}
	removed := -1
	for i := range a.profiles {
		if a.profiles[i].ID == id {
			removed = i
			a.profiles = append(a.profiles[:i], a.profiles[i+1:]...)
			break
		}
	}
	if a.profilesPath != "" {
		if err := saveProfiles(a.profilesPath, a.profiles); err != nil {
			a.setStatusErr(err)
		}
	}
	for _, kind := range []string{"redis", "ssh", "ssh-passphrase", "sentinel", "proxy"} {
		if err := secretDelete("profile/" + id + "/" + kind); err != nil {
			a.setStatusErr(err)
		}
	}
	delete(a.secretCache, id)
	if a.selProfile == id {
		a.selProfile = ""
		a.connSel = -1
		a.form.reset()
	} else if removed >= 0 && a.connSel > removed {
		a.connSel--
	}
}

// testConn opens a throwaway Store and asks for its INFO, for the Test
// connection button.
func testConn(cfg ConnConfig) (string, error) {
	var st Store
	if err := st.Connect(cfg); err != nil {
		return "", err
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	// Connect already pinged; INFO is only for the version line.
	info, _ := st.ServerInfo(ctx)
	return info, nil
}

func (a *App) testConnection() {
	if a.connTesting || a.connConnecting {
		return
	}
	p := a.form.profile(a.selProfile)
	if msg := validateProfile(p); msg != "" {
		a.connErr = message{key: msg}
		return
	}
	secrets, err := a.profileSecrets(p)
	if err != nil {
		if !a.form.hasSecretInput() {
			a.connErr = msg("Could not read the saved password: %s", err)
			return
		}
		secrets = SecretSet{}
	}
	secrets = a.form.mergeSecrets(secrets)
	cfg := p.config(secrets)
	a.connErr = message{}
	a.connTesting = true
	a.connTestResult = message{}
	a.connTestErr = false
	go func() {
		info, err := testConn(cfg)
		a.update(func() {
			a.connTesting = false
			if err != nil {
				a.connTestResult = msg("Test failed: %s", err)
				a.connTestErr = true
				return
			}
			if info != "" {
				a.connTestResult = msg("Connected · %s", info)
			} else {
				a.connTestResult = msg("Connection succeeded")
			}
			a.connTestErr = false
		})
	}()
}

func (a *App) connectSelected() {
	p := a.form.profile(a.selProfile)
	if msg := validateProfile(p); msg != "" {
		a.afterConnect = nil
		a.connErr = message{key: msg}
		return
	}
	if p.ID == "" {
		p.ID = newProfileID()
	}
	if i := a.sessionIndex(p.ID); i >= 0 {
		a.active = i
		a.connErr = message{}
		a.clearStatus()
		return
	}
	a.connConnecting = true
	a.connErr = message{}
	a.connTestResult = message{}
	a.connTestErr = false
	a.clearStatus()
	secrets, err := a.profileSecrets(p)
	if err != nil {
		if !a.form.hasSecretInput() {
			a.connConnecting = false
			a.afterConnect = nil
			a.connErr = msg("Could not read the saved password: %s", err)
			return
		}
		secrets = SecretSet{}
	}
	secrets = a.form.mergeSecrets(secrets)
	cfg := p.config(secrets)
	go func() {
		var st Store
		err := st.Connect(cfg)
		var info string
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			info, _ = st.ServerInfo(ctx)
			cancel()
		}
		a.update(func() {
			a.connConnecting = false
			if err != nil {
				a.afterConnect = nil
				a.connErr = errorMessage(err)
				return
			}
			a.connectedTo(p, st, info, secrets)
		})
	}()
}

// connectedTo adopts a successful connection and saves its profile. It is
// separate from connectSelected so a test can exercise it without a window.
func (a *App) connectedTo(p Profile, st Store, info string, secrets SecretSet) {
	if a.closed {
		st.Close()
		return
	}
	a.selProfile = p.ID
	a.connErr = message{}
	s := a.openSession(p, st, info)
	if err := a.upsertProfile(p); err != nil {
		s.setStatusErr(err)
	}
	a.connSel = -1
	for i := range a.profiles {
		if a.profiles[i].ID == p.ID {
			a.connSel = i
			break
		}
	}
	if err := a.storeSecrets(p.ID, p, secrets); err != nil {
		s.setStatusErr(localError("connected, but a secret could not be stored: %s", err))
	}
	a.clearStatus()
	s.refreshDatabases()
	s.startScan()
	if after := a.afterConnect; after != nil {
		a.afterConnect = nil
		after(s)
	}
}

// visibleConnProfiles lists filtered profiles in grouped order. Keyboard
// selection opens a collapsed group so the chosen row is visible.
func (a *App) visibleConnProfiles() []int {
	var out []int
	for _, g := range a.connGroups() {
		out = append(out, g.profiles...)
	}
	return out
}

// selectConnOffset moves the form selection through visible connections.
func (a *App) selectConnOffset(delta int) {
	list := a.visibleConnProfiles()
	if len(list) == 0 {
		return
	}
	idx := -1
	for i, profileIndex := range list {
		if profileIndex == a.connSel {
			idx = i
			break
		}
	}
	next := 0
	switch {
	case idx < 0 && delta < 0:
		next = len(list) - 1
	case idx < 0:
		next = 0
	default:
		next = idx + delta
		if next < 0 {
			next = 0
		}
		if next >= len(list) {
			next = len(list) - 1
		}
	}
	i := list[next]
	p := a.profiles[i]
	a.connSel = i
	a.selProfile = p.ID
	a.connCollapsed[p.Group] = false
	a.fillConnForm(p)
	a.connErr = message{}
}

// cycleSession moves to the next or previous open connection tab.
func (a *App) cycleSession(delta int) {
	if len(a.sessions) == 0 {
		return
	}
	if a.active < 0 {
		a.active = 0
	} else {
		a.active = (a.active + delta + len(a.sessions)) % len(a.sessions)
	}
	a.stopOtherLive(a.active)
}
