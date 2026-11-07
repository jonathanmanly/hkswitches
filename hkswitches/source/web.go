package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed index.html
var indexHTML []byte

type switchView struct {
	SwitchConfig
	On bool `json:"on"`
}

type statusView struct {
	Version    string       `json:"version"`
	BridgeName string       `json:"bridge_name"`
	Pin        string       `json:"pin"`
	SetupURI   string       `json:"setup_uri"`
	Paired     bool         `json:"paired"`
	Error      string       `json:"error,omitempty"`
	Switches   []switchView `json:"switches"`
}

type switchInput struct {
	Name           *string  `json:"name"`
	AutoOffSeconds *float64 `json:"auto_off_seconds"`
	Reverse        *bool    `json:"reverse"`
	RememberState  *bool    `json:"remember_state"`
}

func (m *Manager) webHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	mux.HandleFunc("GET /qr.png", func(w http.ResponseWriter, r *http.Request) {
		cfg, _, _, _ := m.snapshot()
		png, err := qrcode.Encode(setupURI(cfg.Pin, cfg.SetupID), qrcode.Medium, 256)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(png)
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, m.status())
	})

	// Add a switch.
	mux.HandleFunc("POST /api/switches", func(w http.ResponseWriter, r *http.Request) {
		var in switchInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == nil {
			httpErr(w, 400, "send JSON with at least a name")
			return
		}
		m.opMu.Lock()
		defer m.opMu.Unlock()
		cfg, _, _, _ := m.snapshot()
		sc := SwitchConfig{ID: cfg.NextID, RememberState: true}
		applyInput(&sc, in)
		cfg.NextID++
		cfg.Switches = append(cfg.Switches, sc)
		if err := m.applyConfig(cfg, true); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, m.status())
	})

	// Change a switch's settings.
	mux.HandleFunc("PUT /api/switches/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in switchInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpErr(w, 400, "invalid JSON")
			return
		}
		m.opMu.Lock()
		defer m.opMu.Unlock()
		cfg, _, _, _ := m.snapshot()
		i := cfg.find(id)
		if i < 0 {
			httpErr(w, 404, "no such switch")
			return
		}
		old := cfg.Switches[i]
		applyInput(&cfg.Switches[i], in)
		renamed := strings.TrimSpace(old.Name) != strings.TrimSpace(cfg.Switches[i].Name)
		if err := m.applyConfig(cfg, renamed); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, m.status())
	})

	mux.HandleFunc("DELETE /api/switches/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		m.opMu.Lock()
		defer m.opMu.Unlock()
		cfg, _, _, _ := m.snapshot()
		i := cfg.find(id)
		if i < 0 {
			httpErr(w, 404, "no such switch")
			return
		}
		cfg.Switches = append(cfg.Switches[:i], cfg.Switches[i+1:]...)
		if err := m.applyConfig(cfg, true); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		m.mu.Lock()
		delete(m.state, id)
		m.saveStateLocked()
		m.mu.Unlock()
		writeJSON(w, 200, m.status())
	})

	// Flip switches: POST /api/switches/{id}/on | off | toggle
	for _, action := range []string{"on", "off", "toggle"} {
		action := action
		mux.HandleFunc("POST /api/switches/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			id, ok := pathID(w, r)
			if !ok {
				return
			}
			var found bool
			switch action {
			case "on":
				found = m.Set(id, true)
			case "off":
				found = m.Set(id, false)
			default:
				found = m.Toggle(id)
			}
			if !found {
				httpErr(w, 404, "no such switch")
				return
			}
			writeJSON(w, 200, m.status())
		})
	}

	// Rename the bridge.
	mux.HandleFunc("PUT /api/bridge", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			BridgeName string `json:"bridge_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.BridgeName) == "" {
			httpErr(w, 400, "bridge_name is required")
			return
		}
		m.opMu.Lock()
		defer m.opMu.Unlock()
		cfg, _, _, _ := m.snapshot()
		cfg.BridgeName = strings.TrimSpace(in.BridgeName)
		if err := m.applyConfig(cfg, true); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, m.status())
	})

	return m.auth(mux)
}

// auth enables HTTP Basic auth (any username) when web_password is set.
func (m *Manager) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		pw := m.cfg.WebPassword
		m.mu.Unlock()
		if pw != "" {
			_, got, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(pw)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="hkswitches"`)
				http.Error(w, "password required", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) status() statusView {
	cfg, st, paired, lastErr := m.snapshot()
	v := statusView{
		Version:    version,
		BridgeName: cfg.BridgeName,
		Pin:        formatPin(cfg.Pin),
		SetupURI:   setupURI(cfg.Pin, cfg.SetupID),
		Paired:     paired,
		Error:      lastErr,
		Switches:   []switchView{},
	}
	for _, s := range cfg.Switches {
		v.Switches = append(v.Switches, switchView{s, st[s.ID]})
	}
	return v
}

func applyInput(sc *SwitchConfig, in switchInput) {
	if in.Name != nil {
		sc.Name = strings.TrimSpace(*in.Name)
	}
	if in.AutoOffSeconds != nil {
		sc.AutoOffSeconds = *in.AutoOffSeconds
	}
	if in.Reverse != nil {
		sc.Reverse = *in.Reverse
	}
	if in.RememberState != nil {
		sc.RememberState = *in.RememberState
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, 400, "bad switch id")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
