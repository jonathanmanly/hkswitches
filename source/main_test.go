package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brutella/hap/accessory"
)

// testManager builds a Manager with one switch, without starting HomeKit.
func testManager(t *testing.T, sc SwitchConfig) (*Manager, *accessory.Switch) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Switches = []SwitchConfig{sc}
	cfg.NextID = sc.ID + 1
	m := &Manager{
		dataDir:    dir,
		configPath: filepath.Join(dir, "config.json"),
		statePath:  filepath.Join(dir, "state.json"),
		cfg:        cfg,
		state:      map[uint64]bool{},
		accs:       map[uint64]*accessory.Switch{},
		timers:     map[uint64]*time.Timer{},
	}
	m.mu.Lock()
	sw := m.newSwitchLocked(sc)
	m.mu.Unlock()
	t.Cleanup(func() { m.stopHAP() })
	return m, sw
}

// homekitWrite simulates a write from the Home app (non-nil request).
func homekitWrite(sw *accessory.Switch, v bool) {
	sw.Switch.On.SetValueRequest(v, httptest.NewRequest("PUT", "/characteristics", nil))
}

func isOn(m *Manager, id uint64) bool {
	_, st, _, _ := m.snapshot()
	return st[id]
}

// Auto-off 500ms. Turn on at 0, write "on" again at 300ms, look at 650ms:
// with the option the timer restarted (off at ~800ms), without it the
// switch already went off at ~500ms.
func TestRepeatWriteRestartsTimer(t *testing.T) {
	for _, restart := range []bool{false, true} {
		sc := SwitchConfig{ID: 2, Name: "Room", AutoOffSeconds: 0.5, RestartTimerOnRepeat: restart}
		m, sw := testManager(t, sc)

		homekitWrite(sw, true)
		time.Sleep(300 * time.Millisecond)
		homekitWrite(sw, true)
		time.Sleep(350 * time.Millisecond)
		if got := isOn(m, 2); got != restart {
			t.Errorf("restart=%v: at 650ms switch on=%v, want %v", restart, got, restart)
		}
		time.Sleep(400 * time.Millisecond)
		if isOn(m, 2) {
			t.Errorf("restart=%v: switch still on at 1050ms", restart)
		}
	}
}

// Turning the option on through applyConfig (the web page's live path)
// takes effect on the running accessory.
func TestRestartOptionAppliesLive(t *testing.T) {
	sc := SwitchConfig{ID: 2, Name: "Room", AutoOffSeconds: 0.5}
	m, sw := testManager(t, sc)

	cfg, _, _, _ := m.snapshot()
	cfg.Switches[0].RestartTimerOnRepeat = true
	if err := m.applyConfig(cfg, false); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	same := m.accs[2] == sw
	m.mu.Unlock()
	if !same {
		t.Fatal("accessory was rebuilt; expected a live change")
	}

	homekitWrite(sw, true)
	time.Sleep(300 * time.Millisecond)
	homekitWrite(sw, true)
	time.Sleep(350 * time.Millisecond)
	if !isOn(m, 2) {
		t.Error("repeat write didn't restart the timer after a live config change")
	}
}

// A config.json from before restart_timer_on_repeat existed still loads.
func TestOldConfigLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{"bridge_name":"B","pin":"03145154","setup_id":"ABCD","hap_port":51830,"web_port":8080,
	         "next_id":3,"switches":[{"id":2,"name":"S","auto_off_seconds":900,"reverse":false,"remember_state":true}]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, created, err := loadConfig(path)
	if err != nil || created {
		t.Fatalf("loadConfig: created=%v err=%v", created, err)
	}
	if s := cfg.Switches[0]; s.RestartTimerOnRepeat || s.AutoOffSeconds != 900 || !s.RememberState {
		t.Errorf("unexpected switch config %+v", s)
	}
}
