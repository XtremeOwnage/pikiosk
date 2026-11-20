package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/XtremeOwnage/pikiosk/internal/audio"
	"github.com/XtremeOwnage/pikiosk/internal/config"
)

// BackupFormat identifies pikiosk backup files.
const BackupFormat = "pikiosk-backup"

// maxBackup bounds a restore upload: the settings plus every sound.
const maxBackup = 256 << 20

// Backup is the exported file: the configuration and the uploaded sounds.
// Without secrets, passwords, tokens and header values are "********" and
// the admin password and API token are left out.
type Backup struct {
	Format        string        `json:"format"`
	FormatVersion int           `json:"format_version"`
	Created       time.Time     `json:"created"`
	Version       string        `json:"pikiosk_version"`
	Secrets       bool          `json:"secrets"`
	Config        config.Config `json:"config"`
	Sounds        []audio.File  `json:"sounds"`
}

func (s *Server) exportBackup(w http.ResponseWriter, r *http.Request) {
	secrets := r.URL.Query().Get("secrets") != "0"
	cfg := s.Config.Get()
	if !secrets {
		cfg = cfg.Redact()
	}
	sounds, err := s.Audio.UserFiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	b := Backup{Format: BackupFormat, FormatVersion: 1, Created: time.Now().UTC(), Version: s.Version, Secrets: secrets, Config: cfg, Sounds: sounds}
	if b.Sounds == nil {
		b.Sounds = []audio.File{}
	}
	name := fmt.Sprintf("pikiosk-%s-%s.json", cfg.DeviceID, time.Now().Format("20060102-1504"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(b)
	s.Kiosk.Emit("backup", "exported"+map[bool]string{true: " with secrets", false: ""}[secrets])
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Backup   Backup   `json:"backup"`
		Sections []string `json:"sections"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackup))
	if err := dec.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("not a readable backup: %w", err))
		return
	}
	b := body.Backup
	if b.Format != BackupFormat {
		writeErr(w, http.StatusBadRequest, errors.New("this file is not a pikiosk backup"))
		return
	}
	if b.FormatVersion > 1 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("this backup is from a newer pikiosk (%s); update first", b.Version))
		return
	}
	if len(body.Sections) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("pick at least one part to restore"))
		return
	}
	if err := s.Config.Update(func(c *config.Config) error { return c.Restore(b.Config, body.Sections) }); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	restored := 0
	if slices.Contains(body.Sections, "sounds") {
		for _, f := range b.Sounds {
			if _, err := s.Audio.Save(f.Name, bytes.NewReader(f.Data)); err != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("settings restored, but sound %s failed: %w", f.Name, err))
				return
			}
			restored++
		}
		if restored > 0 {
			s.MQTT.PublishSounds()
		}
	}
	s.Kiosk.Emit("backup", fmt.Sprintf("restored %v", body.Sections))
	writeJSON(w, map[string]any{"ok": true, "sections": body.Sections, "sounds": restored})
}
