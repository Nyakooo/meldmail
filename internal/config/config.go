package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Addr, DBPath, AdminPassword string
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
	key, err := hex.DecodeString(os.Getenv("MELDMAIL_MASTER_KEY"))
	if err != nil || len(key) != 32 {
		return Config{}, fmt.Errorf("config: MELDMAIL_MASTER_KEY must be 64 hex characters")
	}
	c.MasterKey = key
	if v := os.Getenv("MELDMAIL_TRUST_PROXY"); v != "" {
		c.TrustProxy, err = strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config trust proxy: %w", err)
		}
	}
	return c, nil
}
