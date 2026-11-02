package app

import (
	"runtime"

	"github.com/egoist/mygo"
)

func (a *App) applicationMenu() *mygo.Menu {
	items := []*mygo.MenuItem{
		{Role: mygo.RoleAbout},
		{ID: "check-updates", Label: a.tr("Check for updates"), Click: func(*mygo.MenuItem, *mygo.Window) { a.checkUpdates() }},
		mygo.Separator(),
		{Label: a.tr("Preferences"), Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.openPreferences() }},
	}
	if runtime.GOOS == "darwin" {
		items = append(items, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleServices},
			mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleHide},
			&mygo.MenuItem{Role: mygo.RoleHideOthers}, &mygo.MenuItem{Role: mygo.RoleUnhide})
	}
	items = append(items, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Kivrix", Submenu: items},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleViewMenu},
		{Role: mygo.RoleWindowMenu},
	})
}
