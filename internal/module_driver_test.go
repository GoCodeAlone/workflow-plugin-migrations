package internal

import (
	"testing"
)

func TestDriverModule_MissingDriver(t *testing.T) {
	_, err := newDriverModule("test", map[string]any{})
	if err == nil {
		t.Error("expected error for missing driver")
	}
}

func TestDriverModule_InvalidDriver(t *testing.T) {
	_, err := newDriverModule("test", map[string]any{"driver": "unsupported"})
	if err == nil {
		t.Error("expected error for unsupported driver")
	}
}

func TestDriverModule_ValidDrivers(t *testing.T) {
	for _, name := range []string{"golang-migrate", "goose", "atlas"} {
		m, err := newDriverModule("test-"+name, map[string]any{"driver": name})
		if err != nil {
			t.Errorf("newDriverModule(%q): %v", name, err)
			continue
		}
		if err := m.Init(); err != nil {
			t.Errorf("Init(%q): %v", name, err)
		}
	}
}
