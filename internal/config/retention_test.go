package config

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestRetentionIsReadAndWrittenInDays(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"3d": 72 * time.Hour, "90d": 90 * 24 * time.Hour, "0": 0, "36h": 36 * time.Hour, "90m": 90 * time.Minute,
	} {
		r, err := ParseRetention(in)
		if err != nil || r.Duration() != want {
			t.Errorf("ParseRetention(%q) = %v, %v; want %v", in, r.Duration(), err, want)
		}
	}
	for _, bad := range []string{"", "d", "-1d", "1.5d", "soon", "4000d"} {
		if _, err := ParseRetention(bad); err == nil {
			t.Errorf("ParseRetention(%q) accepted", bad)
		}
	}
	if got := DefaultRetention1m.String() + " " + DefaultRetention15m.String() + " " + Retention(0).String(); got != "3d 90d 0" {
		t.Errorf("String = %q", got)
	}
}

func TestRetentionDefaultsAndSources(t *testing.T) {
	cfg, err := Load(nil, env(nil), io.Discard)
	if err != nil || cfg.MetricsRetention1m != DefaultRetention1m || cfg.MetricsRetention15m != DefaultRetention15m {
		t.Fatalf("defaults = %v %v, %v", cfg.MetricsRetention1m, cfg.MetricsRetention15m, err)
	}
	file := writeFile(t, "metrics_retention_1m: 7d\nmetrics_retention_15m: 0\n")
	cfg, err = Load([]string{"-config", file}, env(map[string]string{"REGULUS_METRICS_RETENTION_1M": "2d"}), io.Discard)
	if err != nil || cfg.MetricsRetention1m.Duration() != 48*time.Hour || cfg.MetricsRetention15m != 0 {
		t.Errorf("file and environment = %v %v, %v", cfg.MetricsRetention1m, cfg.MetricsRetention15m, err)
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_METRICS_RETENTION_15M": "lots"}), io.Discard); err == nil || !strings.Contains(err.Error(), "RETENTION_15M") {
		t.Errorf("an invalid environment value: %v", err)
	}
	if _, err := Load([]string{"-config", writeFile(t, "metrics_retention_1m: lots\n")}, env(nil), io.Discard); err == nil {
		t.Error("an invalid file value was accepted")
	}
}
