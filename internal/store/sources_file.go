package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"gopkg.in/yaml.v3"
)

// LoadSources reads plugin_sources from the hub's configuration file, for the
// root helper, which must know the signer of a custom source without taking it
// from anything the hub's user can write. A missing file is no custom source.
// With requireRoot the file must be owned by root and not writable by anyone
// else: a file the hub's user could change would let it name its own signer.
func LoadSources(path string, requireRoot bool) ([]Source, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%s is writable by others than its owner (mode %o)", path, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && requireRoot && st.Uid != 0 {
		return nil, fmt.Errorf("%s is not owned by root", path)
	}
	data, err := readRegular(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var c struct {
		PluginSources []Source `yaml:"plugin_sources"`
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateSources(c.PluginSources); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c.PluginSources, nil
}
