package app

// Connection groups and duplication.

import (
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) openGroupRename(group string) {
	a.groupCreating = false
	a.groupRenameOpen = true
	a.groupRenameFrom = group
	a.groupRenameTo = group
	a.groupRenameErr = ""
}

func (a *App) viewGroupRename(c *ui.Context) {
	if !a.groupRenameOpen {
		return
	}
	pal := a.paletteFor(c)
	title := "Rename group"
	if a.groupCreating {
		title = "New group"
	}
	ui.Modal(c.Key("group-rename"), &a.groupRenameOpen, func() {
		ui.Column(c).Width(400).Padding(4).Gap(10).Children(func() {
			ui.Text(c, a.tr(title)).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Field(c, a.tr("Group name"), func() {
				ui.TextInput(c, &a.groupRenameTo).Width(300)
			})
			if a.groupRenameErr != "" {
				ui.Text(c, a.tr(a.groupRenameErr)).FontSize(11).TextColor(pal.danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "group-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					a.groupRenameOpen = false
				}
				if textButton(c, "group-save", a.tr(title), true, false, pal).Clicked() {
					a.renameGroup(a.groupRenameFrom, a.groupRenameTo)
				}
			})
		})
	})
}

func (a *App) renameGroup(from, to string) {
	to = strings.TrimSpace(to)
	if a.groupCreating {
		if to == "" {
			a.groupRenameErr = "A group name is required"
			return
		}
		if slices.Contains(a.groupNames(), to) {
			a.groupRenameErr = "Group already exists"
			return
		}
		a.prefs.Groups = append(a.groupNames(), to)
		a.savePrefs()
		a.groupRenameOpen, a.groupCreating = false, false
		return
	}
	if to == from {
		a.groupRenameOpen = false
		return
	}
	for i := range a.profiles {
		if a.profiles[i].Group == from {
			a.profiles[i].Group = to
		}
	}
	if a.selProfile != "" && a.form.group == from {
		a.form.group = to
	}
	groups := a.groupNames()
	for i, name := range groups {
		if name == from {
			groups[i] = to
		}
	}
	a.prefs.Groups = uniqueGroups(groups)
	a.savePrefs()
	delete(a.connCollapsed, from)
	sortProfiles(a.profiles)
	if a.profilesPath != "" {
		if err := saveProfiles(a.profilesPath, a.profiles); err != nil {
			a.groupRenameErr = err.Error()
			return
		}
	}
	a.groupRenameOpen = false
	a.setStatus("Moved connections to %q", groupLabel(to))
}

func (a *App) deleteGroup(group string) {
	if group == "" {
		return
	}
	for i := range a.profiles {
		if a.profiles[i].Group == group {
			a.profiles[i].Group = ""
		}
	}
	if a.selProfile != "" && a.form.group == group {
		a.form.group = ""
	}
	a.prefs.Groups = slices.DeleteFunc(a.groupNames(), func(name string) bool { return name == group })
	a.savePrefs()
	delete(a.connCollapsed, group)
	sortProfiles(a.profiles)
	if a.profilesPath != "" {
		if err := saveProfiles(a.profilesPath, a.profiles); err != nil {
			a.setStatusErr(err)
			return
		}
	}
	a.setStatus("Removed group %q", group)
}

// copyProfileSecrets copies every saved secret from one profile to
// another, for Duplicate.
func copyProfileSecrets(from, to string) error {
	for _, kind := range []string{"redis", "ssh", "ssh-passphrase", "sentinel", "proxy"} {
		v := loadSecret("profile/" + from + "/" + kind)
		if v == "" {
			continue
		}
		if err := saveSecret("profile/"+to+"/"+kind, v); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) duplicateProfile(id string) {
	var src Profile
	found := false
	for _, p := range a.profiles {
		if p.ID == id {
			src, found = p, true
			break
		}
	}
	if !found {
		return
	}
	p := src
	p.ID = newProfileID()
	p.Name = src.label() + " copy"
	a.profiles = append(a.profiles, p)
	sortProfiles(a.profiles)
	if err := copyProfileSecrets(src.ID, p.ID); err != nil {
		a.setStatusErr(localError("duplicated, but a secret could not be copied: %s", err))
		return
	}
	if a.profilesPath != "" {
		if err := saveProfiles(a.profilesPath, a.profiles); err != nil {
			a.setStatusErr(err)
			return
		}
	}
	a.selProfile = p.ID
	for i := range a.profiles {
		if a.profiles[i].ID == p.ID {
			a.connSel = i
			a.fillConnForm(a.profiles[i])
			break
		}
	}
	a.connErr = message{}
	a.setStatus("Duplicated connection")
}

func (a *App) askDeleteProfile(id string) {
	a.connDeleteID = id
	a.connDeleteOpen = true
}

func (a *App) viewConnDelete(c *ui.Context) {
	if !a.connDeleteOpen {
		return
	}
	name := a.connDeleteID
	for _, p := range a.profiles {
		if p.ID == a.connDeleteID {
			name = p.label()
			break
		}
	}
	switch ui.AlertDialog(c, &a.connDeleteOpen, a.tr("Delete connection?"),
		a.tr("Delete %q? Its saved passwords will be removed too.", name),
		a.tr("Cancel"), a.tr("Delete")) {
	case 1:
		a.deleteProfile(a.connDeleteID)
		a.connDeleteID = ""
	}
}

func uniqueGroups(names []string) []string {
	out := []string{}
	for _, name := range names {
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func (a *App) groupNames() []string {
	out := uniqueGroups(a.prefs.Groups)
	var extra []string
	for _, p := range a.profiles {
		if p.Group != "" && !slices.Contains(out, p.Group) && !slices.Contains(extra, p.Group) {
			extra = append(extra, p.Group)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func (a *App) moveGroup(name string, delta int) {
	names := a.groupNames()
	i := slices.Index(names, name)
	if i < 0 || i+delta < 0 || i+delta >= len(names) {
		return
	}
	names[i], names[i+delta] = names[i+delta], names[i]
	a.prefs.Groups = names
	a.savePrefs()
}

func (a *App) moveProfile(id string, delta int) {
	p, ok := a.findProfile(id)
	if !ok {
		return
	}
	var indices []int
	current := -1
	for i, item := range a.profiles {
		if item.Group == p.Group {
			if item.ID == id {
				current = len(indices)
			}
			indices = append(indices, i)
		}
	}
	if current < 0 || current+delta < 0 || current+delta >= len(indices) {
		return
	}
	indices[current], indices[current+delta] = indices[current+delta], indices[current]
	for rank, i := range indices {
		a.profiles[i].Order = rank + 1
	}
	sortProfiles(a.profiles)
	for i, item := range a.profiles {
		if item.ID == a.selProfile {
			a.connSel, a.form.order = i, item.Order
		}
	}
	if a.profilesPath != "" {
		if err := saveProfiles(a.profilesPath, a.profiles); err != nil {
			a.setStatusErr(err)
		}
	}
}
