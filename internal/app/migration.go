package app

// One-time carry-over from the app's former name. MyGo's user-data
// directory is named after the app, so Redis Pilot -> Kivrix would
// otherwise start with empty profiles, preferences and local secrets.

import (
	"errors"
	"os"
	"path/filepath"
)

const legacyAppName = "Redis Pilot"

// migrateLegacyUserData copies files that are missing from dir out of the
// former Redis Pilot directory beside it. It never overwrites the new
// directory and never removes the old one.
func migrateLegacyUserData(dir string) error {
	if dir == "" {
		return nil
	}
	parent, base := filepath.Dir(dir), filepath.Base(dir)
	oldNames := []string{legacyAppName, legacyAppName + " Dev"}
	for _, oldName := range oldNames {
		if oldName == base {
			continue
		}
		if err := copyMissingFiles(filepath.Join(parent, oldName), dir); err != nil {
			return err
		}
	}
	return nil
}

func copyMissingFiles(from, to string) error {
	entries, err := os.ReadDir(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		src := filepath.Join(from, entry.Name())
		dst := filepath.Join(to, entry.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := readBoundedFile(src, maxProfileBytes)
		if err != nil {
			return err
		}
		if err := writePrivateFile(dst, data); err != nil {
			return err
		}
	}
	return nil
}
