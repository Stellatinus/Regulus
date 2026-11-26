package plugin

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Errors of the UI side of a plugin.
var (
	// ErrNoPlugin is returned for an id that names no installed plugin (or
	// that is not a plugin id at all, so it can never reach the file system).
	ErrNoPlugin = errors.New("no such plugin")
	// ErrNoUI is returned for a plugin that has no ui component.
	ErrNoUI = errors.New("the plugin has no ui module")
	// ErrUnsafeModule is returned for a module that cannot be put in a page
	// without risk (it could close its own <script> element).
	ErrUnsafeModule = errors.New("the ui module contains </script or <!--")
)

// maxModule bounds a ui module. A widget is one small file.
const maxModule = 2 << 20

// UIPlugin is an installed plugin that has a ui component.
type UIPlugin struct {
	Manifest Manifest
	// Root is the directory that holds the files of the version that is
	// installed: <plugins dir>/<id>/current when that exists (versions kept
	// side by side), else <plugins dir>/<id>.
	Root string
}

// root returns where the files of a plugin are. It does not check that the
// directory exists.
func root(dir, id string) string {
	base := filepath.Join(dir, id)
	cur := filepath.Join(base, "current")
	if _, err := os.Lstat(cur); err == nil {
		return cur
	}
	return base
}

// LoadUI reads the manifest of the installed plugin id and checks it has a ui
// component. The id is checked before it is joined to a path.
func LoadUI(dir, id string) (UIPlugin, error) {
	if dir == "" || !idRE.MatchString(id) || len(id) < 2 || len(id) > 40 {
		return UIPlugin{}, ErrNoPlugin
	}
	r := root(dir, id)
	if !within(filepath.Join(dir, id), r) {
		return UIPlugin{}, ErrNoPlugin
	}
	m, err := readManifest(filepath.Join(r, ManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return UIPlugin{}, ErrNoPlugin
		}
		return UIPlugin{}, err
	}
	if m.ID != id {
		return UIPlugin{}, ErrNoPlugin
	}
	if m.Components.UI == nil {
		return UIPlugin{}, ErrNoUI
	}
	return UIPlugin{Manifest: m, Root: r}, nil
}

// ListUI lists the installed plugins that have a ui component, by id. A
// directory that is not a usable plugin is left out (Discover reports those).
func ListUI(dir string) []UIPlugin {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []UIPlugin
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if u, err := LoadUI(dir, e.Name()); err == nil {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

// within reports whether p, once symbolic links are followed, is inside base
// (also with links followed).
func within(base, p string) bool {
	rb, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rb, rp)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ReadModule returns the module's code. The file must be a regular file inside
// the plugin's directory, links followed, and at most 2 MiB.
func (u UIPlugin) ReadModule() ([]byte, error) {
	full := filepath.Join(u.Root, filepath.FromSlash(u.Manifest.Components.UI.Module))
	if !within(u.Root, full) {
		return nil, fmt.Errorf("the ui module is not inside the plugin directory")
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("the ui module is not a regular file")
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxModule+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxModule {
		return nil, fmt.Errorf("the ui module is larger than %d MiB", maxModule>>20)
	}
	return data, nil
}

// bootScript is the only code of the page that is not the plugin's. It applies
// the theme (CSS variables whose names start with --) and the language that
// the dashboard sends, and nothing else. Messages count only when they come
// from the parent window.
const bootScript = `window.addEventListener("message",function(e){var d=e.data;if(!d||d.regulus!==1||e.source!==window.parent)return;` +
	`if((d.type==="init"||d.type==="theme")&&d.theme){var s=document.documentElement.style;for(var k in d.theme){` +
	`if(/^--[a-z0-9-]+$/.test(k)&&typeof d.theme[k]==="string")s.setProperty(k,d.theme[k])}}` +
	`if(d.type==="init"&&typeof d.locale==="string")document.documentElement.lang=d.locale});`

// UIShell builds the page the dashboard puts in a sandboxed frame: the module
// inlined, and the Content-Security-Policy to send with it.
//
// The module is inlined because the frame has an opaque origin (the sandbox
// has no allow-same-origin): it sends no cookie, so it could not fetch a
// file of the hub, and a module that imports other files would fail. The
// policy allows exactly the two scripts of the page, by hash, no network
// (connect-src 'none'), and no framing except by the hub's own pages.
func UIShell(module []byte) (page []byte, csp string, err error) {
	low := strings.ToLower(string(module))
	if strings.Contains(low, "</script") || strings.Contains(low, "<!--") {
		return nil, "", ErrUnsafeModule
	}
	hash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	code := string(module)
	page = []byte(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<style>html,body{margin:0;background:transparent;color:var(--ink,#eee);font:14px/1.5 var(--f-body,system-ui,sans-serif)}</style>` +
		`</head><body><div id="root"></div><script>` + bootScript + `</script><script type="module">` + code + `</script></body></html>`)
	csp = "sandbox allow-scripts; default-src 'none'; script-src " + hash(bootScript) + " " + hash(code) +
		"; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	return page, csp, nil
}

// ModuleCSP is the policy of the raw module file, for a browser that opens its
// address directly: the same empty origin, and no network.
const ModuleCSP = "sandbox allow-scripts; default-src 'none'; script-src 'self'; connect-src 'none'"
