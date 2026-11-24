// Package config reads the TOML config of rook.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nono/rook/internal/loopback"
)

// defaultListen is the address used when the config has no listen key.
const defaultListen = "127.0.0.1:7878"

// Config is the content of the config file.
type Config struct {
	Listen            string   `toml:"listen"`
	Repos             []string `toml:"repos"`
	ShortcutWorkspace string   `toml:"shortcut_workspace"`
	IgnoreReviewers   []string `toml:"ignore_reviewers"`
}

// DefaultPath gives the path of the config file in the user config directory.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rook", "config.toml"), nil
}

// Load reads and checks the config file at path.
func Load(path string) (Config, error) {
	var cfg Config
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if cfg.Listen == "" {
		cfg.Listen = defaultListen
	}
	if err := cfg.check(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) check() error {
	if len(c.Repos) == 0 {
		return errors.New(`repos is empty: give at least one "owner/name"`)
	}
	seen := map[string]bool{}
	for _, r := range c.Repos {
		owner, name, ok := strings.Cut(r, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			return fmt.Errorf("repo %q: want \"owner/name\"", r)
		}
		// GitHub repo names do not depend on case.
		key := strings.ToLower(r)
		if seen[key] {
			return fmt.Errorf("repo %q: listed twice", r)
		}
		seen[key] = true
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if !loopback.Host(host) {
		return fmt.Errorf("listen %q: the host must be a loopback address", c.Listen)
	}
	return nil
}
