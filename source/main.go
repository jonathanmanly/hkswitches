// hkswitches: HomeKit dummy switches for small Linux boards (e.g. Raspberry Pi Zero W).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	haplog "github.com/brutella/hap/log"
)

var version = "1.1.0"

// Manager owns the config, the switch states and the running HomeKit server.
type Manager struct {
	dataDir    string
	configPath string
	statePath  string

	opMu sync.Mutex // serialises restarts and config changes

	mu      sync.Mutex
	cfg     *Config
	state   map[uint64]bool
	accs    map[uint64]*accessory.Switch
	timers  map[uint64]*time.Timer
	server  *hap.Server
	cancel  context.CancelFunc
	done    chan struct{}
	lastErr string
}

func main() {
	dataDir := flag.String("data", "/var/lib/hkswitches", "directory for config.json, state and HomeKit pairing data")
	debug := flag.Bool("debug", false, "verbose HomeKit protocol logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("hkswitches", version)
		return
	}
	if *debug {
		haplog.Debug.Enable()
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("can't create data dir: %v", err)
	}

	m := &Manager{
		dataDir:    *dataDir,
		configPath: filepath.Join(*dataDir, "config.json"),
		statePath:  filepath.Join(*dataDir, "state.json"),
		state:      map[uint64]bool{},
		accs:       map[uint64]*accessory.Switch{},
		timers:     map[uint64]*time.Timer{},
	}

	cfg, created, err := loadConfig(m.configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if created {
		log.Printf("created %s with an example switch", m.configPath)
	}
	m.cfg = cfg
	m.loadState()

	if err := m.startHAP(); err != nil {
		log.Fatalf("homekit: %v", err)
	}
	log.Printf("hkswitches %s: bridge %q with %d switch(es), pairing code %s",
		version, cfg.BridgeName, len(cfg.Switches), formatPin(cfg.Pin))

	web := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.WebPort),
		Handler:           m.webHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("web page on http://<this-pi>:%d/", cfg.WebPort)
		if err := web.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("web: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for s := range sig {
		if s == syscall.SIGHUP {
			log.Printf("SIGHUP: reloading %s", m.configPath)
			if err := m.reloadFromDisk(); err != nil {
				log.Printf("reload failed, keeping old config: %v", err)
			}
			continue
		}
		break
	}

	log.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	web.Shutdown(ctx)
	m.stopHAP()
}

// ---- state persistence ----

func (m *Manager) loadState() {
	b, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var raw map[string]bool
	if json.Unmarshal(b, &raw) != nil {
		return
	}
	for k, v := range raw {
		var id uint64
		if _, err := fmt.Sscanf(k, "%d", &id); err == nil {
			m.state[id] = v
		}
	}
}

// saveStateLocked must be called with m.mu held.
func (m *Manager) saveStateLocked() {
	raw := map[string]bool{}
	for _, s := range m.cfg.Switches {
		if s.RememberState {
			raw[fmt.Sprint(s.ID)] = m.state[s.ID]
		}
	}
	if err := saveJSON(m.statePath, raw); err != nil {
		log.Printf("saving state: %v", err)
	}
}

// ---- HomeKit server lifecycle ----

func (m *Manager) startHAP() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := m.cfg
	bridge := accessory.NewBridge(accessory.Info{
		Name:         cfg.BridgeName,
		Manufacturer: "hkswitches",
		Model:        "Dummy Switch Bridge",
		SerialNumber: "HKS-" + cfg.SetupID,
		Firmware:     version,
	})
	bridge.Id = 1

	m.accs = map[uint64]*accessory.Switch{}
	var list []*accessory.A
	for _, sc := range cfg.Switches {
		sw := m.newSwitchLocked(sc)
		list = append(list, sw.A)
		// Re-arm the auto-reset timer if we restarted while flipped.
		m.armTimerLocked(sc.ID)
	}

	store := hap.NewFsStore(filepath.Join(m.dataDir, "homekit"))
	srv, err := hap.NewServer(store, bridge.A, list...)
	if err != nil {
		return err
	}
	srv.Pin = cfg.Pin
	srv.SetupId = cfg.SetupID
	srv.Addr = fmt.Sprintf(":%d", cfg.HAPPort)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.server, m.cancel, m.done, m.lastErr = srv, cancel, done, ""
	go func() {
		defer close(done)
		if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
			log.Printf("homekit server stopped: %v", err)
			m.mu.Lock()
			m.lastErr = err.Error()
			m.mu.Unlock()
		}
	}()
	m.saveStateLocked()
	return nil
}

// newSwitchLocked builds the accessory for one switch, sets its starting
// value and registers it in m.accs. m.mu must be held.
func (m *Manager) newSwitchLocked(sc SwitchConfig) *accessory.Switch {
	sw := accessory.NewSwitch(accessory.Info{
		Name:         sc.Name,
		Manufacturer: "hkswitches",
		Model:        "Dummy Switch",
		SerialNumber: fmt.Sprintf("HKS-%d", sc.ID),
		Firmware:     version,
	})
	sw.Id = sc.ID

	on := sc.Reverse // resting state
	if sc.RememberState {
		if v, ok := m.state[sc.ID]; ok {
			on = v
		}
	}
	m.state[sc.ID] = on
	sw.Switch.On.SetValue(on)
	sw.Switch.On.SetUpdateOnSameValue(sc.RestartTimerOnRepeat)

	id := sc.ID
	sw.Switch.On.OnValueRemoteUpdate(func(v bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.accs[id] != sw { // callback from an old server instance
			return
		}
		if v == m.state[id] {
			log.Printf("HomeKit set %q -> %s again", sc.Name, onOff(v))
		} else {
			log.Printf("HomeKit set %q -> %s", sc.Name, onOff(v))
		}
		m.changedLocked(id, v)
	})

	m.accs[id] = sw
	return sw
}

func (m *Manager) stopHAP() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	for id, t := range m.timers {
		t.Stop()
		delete(m.timers, id)
	}
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Println("homekit server slow to stop; continuing")
	}
}

func (m *Manager) restartHAP() error {
	m.stopHAP()
	// Give the mDNS responder a moment to send its goodbye packets.
	time.Sleep(300 * time.Millisecond)
	return m.startHAP()
}

// ---- switching logic ----

// changedLocked records a new value and handles auto-reset. m.mu must be held.
// It also runs for a repeat write of the current value (restart_timer_on_repeat),
// which re-arms the timer from zero.
func (m *Manager) changedLocked(id uint64, on bool) {
	same := m.state[id] == on
	m.state[id] = on
	m.armTimerLocked(id)
	if same {
		return // nothing new to save; spares the SD card on every motion event
	}
	if i := m.cfg.find(id); i >= 0 && m.cfg.Switches[i].RememberState {
		m.saveStateLocked()
	}
}

func (m *Manager) armTimerLocked(id uint64) {
	if t, ok := m.timers[id]; ok {
		t.Stop()
		delete(m.timers, id)
	}
	i := m.cfg.find(id)
	if i < 0 {
		return
	}
	sc := m.cfg.Switches[i]
	if sc.AutoOffSeconds <= 0 || m.state[id] == sc.Reverse {
		return
	}
	d := time.Duration(sc.AutoOffSeconds * float64(time.Second))
	var t *time.Timer
	t = time.AfterFunc(d, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.timers[id] != t {
			return // superseded
		}
		delete(m.timers, id)
		log.Printf("auto-reset %q -> %s", sc.Name, onOff(sc.Reverse))
		m.setLocked(id, sc.Reverse)
	})
	m.timers[id] = t
}

// setLocked flips a switch from our side (web page, API, timer) and
// notifies HomeKit. m.mu must be held.
func (m *Manager) setLocked(id uint64, on bool) bool {
	sw, ok := m.accs[id]
	if !ok {
		return false
	}
	sw.Switch.On.SetValue(on)
	m.changedLocked(id, on)
	return true
}

func (m *Manager) Set(id uint64, on bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setLocked(id, on)
}

func (m *Manager) Toggle(id uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setLocked(id, !m.state[id])
}

// ---- config changes ----

// applyConfig validates and saves a new config. If restart is true the
// HomeKit server is restarted so HomeKit sees added/removed/renamed switches.
func (m *Manager) applyConfig(newCfg *Config, restart bool) error {
	newCfg.fillDefaults()
	if err := newCfg.validate(); err != nil {
		return err
	}
	if err := saveJSON(m.configPath, newCfg); err != nil {
		return err
	}
	if restart {
		m.mu.Lock()
		m.cfg = newCfg
		m.mu.Unlock()
		return m.restartHAP()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = newCfg
	for _, s := range newCfg.Switches {
		if sw, ok := m.accs[s.ID]; ok {
			sw.Switch.On.SetUpdateOnSameValue(s.RestartTimerOnRepeat)
		}
		m.armTimerLocked(s.ID)
	}
	m.saveStateLocked()
	return nil
}

func (m *Manager) reloadFromDisk() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	cfg, _, err := loadConfig(m.configPath)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return m.restartHAP()
}

func (m *Manager) snapshot() (*Config, map[uint64]bool, bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := map[uint64]bool{}
	for k, v := range m.state {
		st[k] = v
	}
	paired := m.server != nil && m.server.IsPaired()
	return m.cfg.clone(), st, paired, m.lastErr
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
