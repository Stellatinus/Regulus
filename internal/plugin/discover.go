package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Found is a plugin found in the plugins directory with a valid manifest.
type Found struct {
	Manifest Manifest
	// Dir is where the plugin's files are: <plugins dir>/<id> in the flat
	// layout, or the version <plugins dir>/<id>/current points at (see
	// installed.go).
	Dir string
	// Versioned is true for the layout the store creates.
	Versioned bool
}

// Problem is a directory in the plugins directory that is not a usable
// plugin, and why. It is reported, never run.
type Problem struct {
	Dir string
	Err error
}

func (p Problem) Error() string { return fmt.Sprintf("%s: %v", p.Dir, p.Err) }

// Discover reads the plugins directory: one directory per plugin, named after
// its id, with plugin.yaml at its top, or (the layout of the store) with a
// link current to the version directory that holds it. Anything else in the directory
// (files, hidden entries) is ignored; a directory without a valid manifest is
// a Problem. A missing plugins directory is no plugins.
func Discover(dir string) ([]Found, []Problem, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var found []Found
	var problems []Problem
	for _, e := range entries {
		name := e.Name()
		if name == "" || name[0] == '.' {
			continue
		}
		pdir := filepath.Join(dir, name)
		// Lstat: a link in the plugins directory could point anywhere.
		info, err := os.Lstat(pdir)
		if err != nil || !info.IsDir() {
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				problems = append(problems, Problem{pdir, errors.New("is a symbolic link; a plugin must be a directory")})
			}
			continue
		}
		vdir, versioned, err := resolveVersionDir(pdir)
		if err != nil {
			problems = append(problems, Problem{pdir, err})
			continue
		}
		m, err := readManifest(filepath.Join(vdir, ManifestName))
		if err != nil {
			problems = append(problems, Problem{pdir, err})
			continue
		}
		if m.ID != name {
			problems = append(problems, Problem{pdir, fmt.Errorf("the directory must be named after the plugin id %q", m.ID)})
			continue
		}
		found = append(found, Found{Manifest: m, Dir: vdir, Versioned: versioned})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Manifest.ID < found[j].Manifest.ID })
	return found, problems, nil
}

func readManifest(p string) (Manifest, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("%s is not a regular file", ManifestName)
	}
	f, err := os.Open(p)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = f.Close() }()
	return ParseManifest(f)
}
