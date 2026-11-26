package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Addr, DBPath, AdminPassword string
	HostProxyURL                string
	LogDir                      string
	MasterKey                   []byte
	TrustProxy                  bool
}

func Load() (Config, error) {
	c := Config{Addr: os.Getenv("MELDMAIL_ADDR"), DBPath: os.Getenv("MELDMAIL_DB_PATH"), AdminPassword: os.Getenv("MELDMAIL_ADMIN_PASSWORD")}
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DBPath == "" {
		c.DBPath = "data/meldmail.db"
	}
	c.LogDir = os.Getenv("MELDMAIL_LOG_DIR")
	if c.LogDir == "" {
		c.LogDir = filepath.Join(filepath.Dir(c.DBPath), "logs")
	}
	key, err := hex.DecodeString(os.Getenv("MELDMAIL_MASTER_KEY"))
	if err != nil || len(key) != 32 {
		return Config{}, fmt.Errorf("config: MELDMAIL_MASTER_KEY must be 64 hex characters")
	}
	c.MasterKey = key
	if raw := os.Getenv("MELDMAIL_HOST_PROXY_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("config: MELDMAIL_HOST_PROXY_PORT must be between 1 and 65535")
		}
		c.HostProxyURL = "http://127.0.0.1:" + strconv.Itoa(port)
	}

	if v := os.Getenv("MELDMAIL_TRUST_PROXY"); v != "" {
		c.TrustProxy, err = strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config trust proxy: %w", err)
		}
	}
	return c, nil
}
