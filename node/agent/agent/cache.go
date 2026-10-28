package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type FileCache struct{ Path string }

func (c FileCache) Load() ([]Known, error) {
	b, err := os.ReadFile(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var k []Known
	return k, json.Unmarshal(b, &k)
}

// Save writes through a rename so a crash never leaves a half-written file.
func (c FileCache) Save(k []Known) error {
	b, err := json.Marshal(k)
	if err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}

type MemCache struct{ Data []Known }

func (c *MemCache) Load() ([]Known, error) { return append([]Known(nil), c.Data...), nil }
func (c *MemCache) Save(k []Known) error   { c.Data = append([]Known(nil), k...); return nil }
