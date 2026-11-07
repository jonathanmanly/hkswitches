package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/brutella/hap"
)

// SwitchConfig describes one dummy switch.
type SwitchConfig struct {
	// ID is the HomeKit accessory ID. It is assigned once and never reused,
	// so automations keep working when switches are renamed, added or removed.
	ID uint64 `json:"id"`
	// Name shown in the Home app when the switch is first added.
	Name string `json:"name"`
	// AutoOffSeconds: if > 0, the switch returns to its resting state this
	// many seconds after being flipped. 0 means it stays where you put it.
	AutoOffSeconds float64 `json:"auto_off_seconds"`
	// Reverse makes the resting state ON instead of OFF (an auto-reset switch
	// then turns itself back ON after being turned off).
	Reverse bool `json:"reverse"`
	// RememberState keeps the on/off state across restarts. When false the
	// switch starts in its resting state.
	RememberState bool `json:"remember_state"`
}

// Config is the whole program configuration, stored as config.json.
type Config struct {
	BridgeName  string         `json:"bridge_name"`
	Pin         string         `json:"pin"`
	SetupID     string         `json:"setup_id"`
	HAPPort     int            `json:"hap_port"`
	WebPort     int            `json:"web_port"`
	WebPassword string         `json:"web_password"`
	NextID      uint64         `json:"next_id"`
	Switches    []SwitchConfig `json:"switches"`
}

const maxSwitches = 149 // HomeKit bridges allow 150 accessories, incl. the bridge

func defaultConfig() *Config {
	return &Config{
		BridgeName: "Dummy Switches",
		Pin:        randomPin(),
		SetupID:    randomSetupID(),
		HAPPort:    51830,
		WebPort:    8080,
		NextID:     3,
		Switches: []SwitchConfig{
			{ID: 2, Name: "Example Switch", AutoOffSeconds: 0, RememberState: true},
		},
	}
}

func randomPin() string {
	for {
		n, _ := rand.Int(rand.Reader, big.NewInt(100000000))
		p := fmt.Sprintf("%08d", n.Int64())
		if _, bad := hap.InvalidPins[p]; !bad {
			return p
		}
	}
}

func randomSetupID() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 4)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

// loadConfig reads config.json, creating it with defaults if missing.
func loadConfig(path string) (*Config, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		c := defaultConfig()
		return c, true, saveJSON(path, c)
	}
	if err != nil {
		return nil, false, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, false, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	changed := c.fillDefaults()
	if err := c.validate(); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if changed {
		err = saveJSON(path, &c)
	}
	return &c, false, err
}

// fillDefaults fills in missing values (e.g. after hand-editing) and assigns
// IDs to switches added by hand without one.
func (c *Config) fillDefaults() bool {
	changed := false
	if c.BridgeName == "" {
		c.BridgeName, changed = "Dummy Switches", true
	}
	if c.Pin == "" {
		c.Pin, changed = randomPin(), true
	}
	c.Pin = strings.ReplaceAll(c.Pin, "-", "")
	if c.SetupID == "" {
		c.SetupID, changed = randomSetupID(), true
	}
	if c.HAPPort == 0 {
		c.HAPPort, changed = 51830, true
	}
	if c.WebPort == 0 {
		c.WebPort, changed = 8080, true
	}
	maxID := uint64(1)
	for _, s := range c.Switches {
		if s.ID > maxID {
			maxID = s.ID
		}
	}
	if c.NextID <= maxID {
		c.NextID, changed = maxID+1, true
	}
	for i := range c.Switches {
		if c.Switches[i].ID == 0 {
			c.Switches[i].ID = c.NextID
			c.NextID++
			changed = true
		}
	}
	return changed
}

func (c *Config) validate() error {
	if len(c.Pin) != 8 {
		return fmt.Errorf("pin must be 8 digits")
	}
	for _, r := range c.Pin {
		if r < '0' || r > '9' {
			return fmt.Errorf("pin must be 8 digits")
		}
	}
	if _, bad := hap.InvalidPins[c.Pin]; bad {
		return fmt.Errorf("pin %s is too simple; HomeKit rejects it", c.Pin)
	}
	if len(c.Switches) > maxSwitches {
		return fmt.Errorf("at most %d switches are allowed", maxSwitches)
	}
	seenID := map[uint64]bool{}
	seenName := map[string]bool{}
	for _, s := range c.Switches {
		if s.ID < 2 {
			return fmt.Errorf("switch %q: id must be 2 or higher (1 is the bridge)", s.Name)
		}
		if seenID[s.ID] {
			return fmt.Errorf("two switches share id %d", s.ID)
		}
		seenID[s.ID] = true
		if err := validateName(s.Name); err != nil {
			return err
		}
		k := strings.ToLower(strings.TrimSpace(s.Name))
		if seenName[k] {
			return fmt.Errorf("two switches are named %q", s.Name)
		}
		seenName[k] = true
		if s.AutoOffSeconds < 0 {
			return fmt.Errorf("switch %q: auto_off_seconds can't be negative", s.Name)
		}
	}
	return nil
}

func validateName(n string) error {
	n = strings.TrimSpace(n)
	if n == "" {
		return fmt.Errorf("switch name can't be empty")
	}
	if len(n) > 64 {
		return fmt.Errorf("switch name %q is too long (64 characters max)", n)
	}
	return nil
}

func (c *Config) find(id uint64) int {
	for i, s := range c.Switches {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func (c *Config) clone() *Config {
	cp := *c
	cp.Switches = append([]SwitchConfig(nil), c.Switches...)
	return &cp
}

// saveJSON writes atomically (temp file + rename) so a power cut can't
// leave a half-written file on the SD card.
func saveJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), path)
}

// setupURI builds the X-HM:// payload the Home app reads from a QR code.
func setupURI(pin, setupID string) string {
	var p uint64
	fmt.Sscanf(pin, "%d", &p)
	const category = 2 // bridge
	const flagIP = 2
	payload := uint64(category)<<31 | uint64(flagIP)<<27 | (p & 0x7ffffff)
	s := strings.ToUpper(big.NewInt(0).SetUint64(payload).Text(36))
	for len(s) < 9 {
		s = "0" + s
	}
	return "X-HM://" + s + setupID
}

func formatPin(p string) string {
	if len(p) != 8 {
		return p
	}
	return p[:3] + "-" + p[3:5] + "-" + p[5:]
}
