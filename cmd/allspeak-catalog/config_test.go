package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fullEnv() map[string]string {
	return map[string]string{
		"AUTH_ADMIN_TOKEN": "admin",
		"AUTH_READ_TOKEN":  "read",
		"CF_ACCESS_KEY_ID": "key",
		"CF_ACCESS_SECRET": "secret",
		"CF_ENDPOINT":      "https://acc.r2.cloudflarestorage.com",
		"CF_BUCKET":        "bucket",
		"DB_PATH":          "",
		"LISTEN_ADDR":      "",
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("full env", func(t *testing.T) {
		env := fullEnv()
		env["DB_PATH"] = "/custom/catalog.db"
		env["LISTEN_ADDR"] = ":9090"
		setEnv(t, env)

		cfg, err := loadConfig()
		require.NoError(t, err)
		assert.Equal(t, "admin", cfg.adminToken)
		assert.Equal(t, "read", cfg.readToken)
		assert.Equal(t, "key", cfg.cfKeyID)
		assert.Equal(t, "secret", cfg.cfSecret)
		assert.Equal(t, "https://acc.r2.cloudflarestorage.com", cfg.cfEndpoint)
		assert.Equal(t, "bucket", cfg.cfBucket)
		assert.Equal(t, "/custom/catalog.db", cfg.dbPath)
		assert.Equal(t, ":9090", cfg.listenAddr)
	})

	t.Run("defaults applied", func(t *testing.T) {
		setEnv(t, fullEnv())

		cfg, err := loadConfig()
		require.NoError(t, err)
		assert.Equal(t, defaultDBPath, cfg.dbPath)
		assert.Equal(t, defaultListenAddr, cfg.listenAddr)
	})

	required := []string{
		"AUTH_ADMIN_TOKEN",
		"AUTH_READ_TOKEN",
		"CF_ACCESS_KEY_ID",
		"CF_ACCESS_SECRET",
		"CF_ENDPOINT",
		"CF_BUCKET",
	}
	for _, name := range required {
		t.Run("missing "+name, func(t *testing.T) {
			env := fullEnv()
			env[name] = ""
			setEnv(t, env)

			_, err := loadConfig()
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
		})
	}
}
