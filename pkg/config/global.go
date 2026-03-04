package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GlobalConfig represents ~/.config/roxy/config.yaml.
type GlobalConfig struct {
	Tunnel TunnelConfig `yaml:"tunnel"`
}

// TunnelConfig holds the user's tunnel provider preference.
type TunnelConfig struct {
	Provider string `yaml:"provider"`          // "ngrok", "cloudflared", "bore", "localtunnel", "custom"
	Command  string `yaml:"command,omitempty"` // custom command template (only used when provider is "custom")
}

// globalConfigFile returns the path to config.yaml inside the given config dir.
func globalConfigFile(configDir string) string {
	return filepath.Join(configDir, "config.yaml")
}

// LoadGlobalConfig reads the global config from disk.
// Returns a zero-value config (not an error) if the file doesn't exist.
func LoadGlobalConfig(configDir string) (*GlobalConfig, error) {
	path := globalConfigFile(configDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &GlobalConfig{}, nil
		}
		return nil, err
	}

	var cfg GlobalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveGlobalConfig writes the global config to disk, creating the directory if needed.
func SaveGlobalConfig(configDir string, cfg *GlobalConfig) error {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(globalConfigFile(configDir), data, 0644)
}
