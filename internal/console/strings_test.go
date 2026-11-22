package console

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/config"
)

// fields walks every string (and every string of a string array or map) of a
// table with its field name. SettingInfo is left out: English has none on
// purpose (TestSettingDescriptions covers it).
func fields(t *testing.T, s strs) map[string]string {
	t.Helper()
	out := map[string]string{}
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		f := v.Field(i)
		if name == "SettingInfo" {
			continue
		}
		switch f.Kind() {
		case reflect.String:
			out[name] = f.String()
		case reflect.Array:
			for j := 0; j < f.Len(); j++ {
				out[name+"["+string(rune('0'+j))+"]"] = f.Index(j).String()
			}
		case reflect.Map:
			for _, k := range f.MapKeys() {
				out[name+"["+k.String()+"]"] = f.MapIndex(k).String()
			}
		default:
			t.Fatalf("field %s has an unexpected kind %s", name, f.Kind())
		}
	}
	return out
}

func TestEveryTextExistsInBothLanguages(t *testing.T) {
	en, it := fields(t, en), fields(t, it)
	if len(en) != len(it) {
		t.Errorf("English has %d texts, Italian %d", len(en), len(it))
	}
	for name := range it {
		if _, ok := en[name]; !ok {
			t.Errorf("Italian %s has no English text", name)
		}
	}
	for name, text := range en {
		if strings.TrimSpace(text) == "" {
			t.Errorf("English %s is empty", name)
		}
		if strings.TrimSpace(it[name]) == "" {
			t.Errorf("Italian %s is empty", name)
		}
	}
}

var verbRE = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z%]`)

func TestTemplatesKeepTheirVerbs(t *testing.T) {
	en, it := fields(t, en), fields(t, it)
	for name, text := range en {
		// The same verbs in the same order: %d %s is not %s %d.
		if a, b := verbRE.FindAllString(text, -1), verbRE.FindAllString(it[name], -1); !slices.Equal(a, b) {
			t.Errorf("%s: English has the verbs %q, Italian %q", name, a, b)
		}
	}
}

func TestSettingDescriptions(t *testing.T) {
	if len(en.SettingInfo) != 0 {
		t.Error("English descriptions belong in internal/config, not in the table")
	}
	keys := config.Keys()
	for _, k := range keys {
		if strings.TrimSpace(it.SettingInfo[k]) == "" {
			t.Errorf("the setting %s has no Italian description", k)
		}
	}
	for k := range it.SettingInfo {
		if !slices.Contains(keys, k) {
			t.Errorf("the Italian description of %s is for no setting", k)
		}
	}
}

func TestStatesCoverWhatTheHubReports(t *testing.T) {
	for _, s := range []string{"active", "inactive", "failed", "unknown", "connected", "stale", "offline", "revoked"} {
		if en.States[s] == "" || it.States[s] == "" {
			t.Errorf("no word for the state %q", s)
		}
	}
}

func TestHelpLinesKeepTheirKeys(t *testing.T) {
	// The keys that do something are the same letters in both languages.
	for _, k := range []string{"r ", "p ", "q ", "y ", "n "} {
		join := en.HelpMain + en.HelpReset + en.HelpQuit + en.HelpConfirm
		joinIt := it.HelpMain + it.HelpReset + it.HelpQuit + it.HelpConfirm
		if strings.Contains(join, k) != strings.Contains(joinIt, k) {
			t.Errorf("the key %q is documented in only one language", strings.TrimSpace(k))
		}
	}
}

func TestUseLocale(t *testing.T) {
	defer func() { T = en }()
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, tc := range map[string]struct {
		env  map[string]string
		want strs
	}{
		"nothing set":                    {map[string]string{}, en},
		"English":                        {map[string]string{"LANG": "en_US.UTF-8"}, en},
		"Italian":                        {map[string]string{"LANG": "it_IT.UTF-8"}, it},
		"bare it":                        {map[string]string{"LANG": "it"}, it},
		"LC_ALL wins over LANG":          {map[string]string{"LC_ALL": "en_GB.UTF-8", "LANG": "it_IT.UTF-8"}, en},
		"LC_ALL Italian over English":    {map[string]string{"LC_ALL": "it_IT.UTF-8", "LANG": "en_US.UTF-8"}, it},
		"LC_MESSAGES over LANG":          {map[string]string{"LC_MESSAGES": "it_CH", "LANG": "en_US"}, it},
		"a language that only starts it": {map[string]string{"LANG": "ita_XX"}, en},
		"C":                              {map[string]string{"LANG": "C"}, en},
	} {
		UseLocale(get(tc.env))
		if !reflect.DeepEqual(T, tc.want) {
			t.Errorf("%s: wrong language", name)
		}
	}
	// It always resets: an Italian call, then an empty environment.
	UseLocale(get(map[string]string{"LANG": "it_IT"}))
	UseLocale(get(nil))
	if !reflect.DeepEqual(T, en) {
		t.Error("UseLocale does not reset to English")
	}
}

// Every code internal/config can return has a text in both languages, with
// as many verbs as the code has arguments (#318).
func TestSettingErrorsCoverEveryCode(t *testing.T) {
	codes := config.ErrorCodes()
	for _, table := range []struct {
		name string
		s    strs
	}{{"English", en}, {"Italian", it}} {
		for _, c := range codes {
			tpl := table.s.SettingErrors[string(c)]
			if strings.TrimSpace(tpl) == "" {
				t.Errorf("%s has no text for the setting error %s", table.name, c)
				continue
			}
			want := len(verbRE.FindAllString(config.EnglishError(c), -1))
			if got := len(verbRE.FindAllString(tpl, -1)); got != want {
				t.Errorf("%s %s has %d verbs, the code has %d arguments", table.name, c, got, want)
			}
		}
		if len(table.s.SettingErrors) != len(codes) {
			t.Errorf("%s has %d setting errors, internal/config %d codes", table.name, len(table.s.SettingErrors), len(codes))
		}
	}
}

// #71: the Health tab says when the running hub found a newer release, and
// stays quiet (a plain OK) when it did not.
func TestUpdatesCheck(t *testing.T) {
	c := updatesCheck(Snapshot{UpdateAvailable: "0.4.0"})
	if c.Key != "updates" || c.Status != Warn || !strings.Contains(c.Detail, "0.4.0") || !strings.Contains(c.Text, "0.4.0") {
		t.Errorf("pending: %+v, want a warning that names 0.4.0 in both languages", c)
	}
	if c := updatesCheck(Snapshot{}); c.Status != OK || c.Key != "updates" {
		t.Errorf("none pending: %+v, want OK", c)
	}
}
