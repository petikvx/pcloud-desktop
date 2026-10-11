package ui

import (
	"os"
	"strconv"

	"github.com/zalando/go-keyring"

	"pcloud-desktop/internal/pcloud"
)

const (
	keyringService = "pcloud-desktop"
	keyringUser    = "session"
)

// Le jeton (clé API ou OAuth, jamais le mot de passe) est stocké dans le
// trousseau système.
func saveSession(value string) {
	_ = keyring.Set(keyringService, keyringUser, value)
}

func loadSession() (value string, ok bool) {
	value, err := keyring.Get(keyringService, keyringUser)
	return value, err == nil && value != ""
}

// oauthConfig lit l'application pCloud dans l'environnement : le secret ne
// doit jamais être commité.
func oauthConfig() pcloud.OAuthConfig {
	cfg := pcloud.OAuthConfig{
		ClientID:     os.Getenv("PCLOUD_CLIENT_ID"),
		ClientSecret: os.Getenv("PCLOUD_CLIENT_SECRET"),
		Port:         8765,
	}
	if p, err := strconv.Atoi(os.Getenv("PCLOUD_REDIRECT_PORT")); err == nil && p > 0 {
		cfg.Port = p
	}
	return cfg
}
