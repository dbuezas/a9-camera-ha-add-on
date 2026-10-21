package a9

import (
	"encoding/json"
	"os"
	"sync"
)

// Settings are kept by the server, not by the camera
type Settings struct {
	// Audio false asks the camera for video only, so no audio leaves it
	Audio bool `json:"audio"`
}

var defaultSettings = Settings{Audio: true}

// settingsStore keeps Settings per camera UID in a JSON file. Empty path: memory only.
type settingsStore struct {
	path string
	mu   sync.Mutex
	m    map[string]Settings
}

func (s *settingsStore) load() {
	s.m = map[string]Settings{}
	if s.path == "" {
		return
	}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.m)
	}
}

func (s *settingsStore) get(uid string) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[uid]; ok {
		return v
	}
	return defaultSettings
}

func (s *settingsStore) set(uid string, v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[uid] = v
	if s.path == "" {
		return nil
	}
	b, _ := json.MarshalIndent(s.m, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
