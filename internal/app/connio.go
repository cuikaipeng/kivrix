package app

// Exported profiles omit passwords, but still contain server addresses,
// usernames and local file paths. Imported servers need passwords again.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
)

type profileExport struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}

func marshalProfiles(ps []Profile) ([]byte, error) {
	return json.MarshalIndent(profileExport{Version: 1, Profiles: ps}, "", "  ")
}

func decodeProfiles(data []byte) ([]Profile, error) {
	var wrapper profileExport
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Profiles != nil {
		return wrapper.Profiles, nil
	}
	var list []Profile
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("not a Kivrix connection file: %w", err)
	}
	return list, nil
}

// importProfileList validates imported entries, gives each a new ID and
// merges them into existing. It returns added and skipped counts.
func importProfileList(existing, imported []Profile) ([]Profile, int, int) {
	out := append([]Profile(nil), existing...)
	used := map[string]bool{}
	for _, p := range out {
		used[p.ID] = true
	}
	added, skipped := 0, 0
	for _, p := range imported {
		p.ID = ""
		p.Name = strings.TrimSpace(p.Name)
		p.Group = strings.TrimSpace(p.Group)
		if validateProfile(p) != "" {
			skipped++
			continue
		}
		id := newProfileID()
		for used[id] {
			id = newProfileID()
		}
		used[id] = true
		p.ID = id
		out = append(out, p)
		added++
	}
	sortProfiles(out)
	return out, added, skipped
}

func (a *App) exportProfiles() {
	if a.connIO {
		return
	}
	if len(a.profiles) == 0 {
		a.setStatus("No connections to export")
		return
	}
	a.connIO = true
	a.setStatus("Choose a file for the connection export…")
	profiles := append([]Profile(nil), a.profiles...)
	title, filterName := a.tr("Export connections"), a.tr("JSON files")
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       title,
			DefaultPath: "kivrix-connections.json",
			Filters:     []mygo.FileFilter{{Name: filterName, Extensions: []string{"json"}}},
		})
		if err == nil && path != "" {
			var data []byte
			data, err = marshalProfiles(profiles)
			if err == nil {
				err = writePrivateFile(path, data)
			}
		}
		a.update(func() {
			defer func() { a.connIO = false }()
			if err != nil {
				a.setStatusErr(err)
				return
			}
			if path == "" {
				a.setStatus("Export canceled")
				return
			}
			a.setStatus("Exported %d connections · passwords are not included", len(profiles))
		})
	}()
}

func (a *App) importProfiles() {
	if a.connIO {
		return
	}
	a.connIO = true
	a.setStatus("Choose a connection file to import…")
	title, filterName := a.tr("Import connections"), a.tr("JSON files")
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   title,
			Filters: []mygo.FileFilter{{Name: filterName, Extensions: []string{"json"}}},
		})
		var imported []Profile
		if err == nil && len(paths) > 0 {
			var data []byte
			data, err = readBoundedFile(paths[0], maxProfileBytes)
			if err == nil {
				imported, err = decodeProfiles(data)
			}
		}
		a.update(func() {
			defer func() { a.connIO = false }()
			if err != nil {
				a.setStatusErr(err)
				return
			}
			if len(paths) == 0 {
				a.setStatus("Import canceled")
				return
			}
			old := map[string]bool{}
			for _, p := range a.profiles {
				old[p.ID] = true
			}
			profiles, added, skipped := importProfileList(a.profiles, imported)
			a.profiles = profiles
			if a.profilesPath != "" {
				if err := saveProfiles(a.profilesPath, a.profiles); err != nil {
					a.setStatusErr(err)
					return
				}
			}
			a.setStatus("Imported %d connections · skipped %d · enter passwords again", added, skipped)
			if added > 0 {
				for i, p := range a.profiles {
					if !old[p.ID] {
						a.selProfile, a.connSel = p.ID, i
						a.fillConnForm(p)
						a.connErr = message{}
						break
					}
				}
			}
		})
	}()
}

func (a *App) disconnectAll() {
	a.protectAllEdits(func() {
		for i := len(a.sessions) - 1; i >= 0; i-- {
			a.closeSessionNow(i)
		}
		a.active = -1
		a.setStatus("Disconnected all connections")
	})
}
