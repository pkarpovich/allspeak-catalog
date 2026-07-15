package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultDBPath     = "/data/catalog.db"
	defaultListenAddr = ":8080"
)

type config struct {
	adminToken string
	readToken  string
	cfKeyID    string
	cfSecret   string
	cfEndpoint string
	cfBucket   string
	dbPath     string
	listenAddr string
}

func loadConfig() (config, error) {
	cfg := config{
		adminToken: os.Getenv("AUTH_ADMIN_TOKEN"),
		readToken:  os.Getenv("AUTH_READ_TOKEN"),
		cfKeyID:    os.Getenv("CF_ACCESS_KEY_ID"),
		cfSecret:   os.Getenv("CF_ACCESS_SECRET"),
		cfEndpoint: os.Getenv("CF_ENDPOINT"),
		cfBucket:   os.Getenv("CF_BUCKET"),
		dbPath:     os.Getenv("DB_PATH"),
		listenAddr: os.Getenv("LISTEN_ADDR"),
	}

	required := []struct {
		name  string
		value string
	}{
		{"AUTH_ADMIN_TOKEN", cfg.adminToken},
		{"AUTH_READ_TOKEN", cfg.readToken},
		{"CF_ACCESS_KEY_ID", cfg.cfKeyID},
		{"CF_ACCESS_SECRET", cfg.cfSecret},
		{"CF_ENDPOINT", cfg.cfEndpoint},
		{"CF_BUCKET", cfg.cfBucket},
	}
	var missing []string
	for _, r := range required {
		if r.value == "" {
			missing = append(missing, r.name)
		}
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if cfg.adminToken == cfg.readToken {
		return config{}, fmt.Errorf("AUTH_ADMIN_TOKEN and AUTH_READ_TOKEN must differ")
	}

	if cfg.dbPath == "" {
		cfg.dbPath = defaultDBPath
	}
	if cfg.listenAddr == "" {
		cfg.listenAddr = defaultListenAddr
	}
	return cfg, nil
}
