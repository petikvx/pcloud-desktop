package ui

import (
	"github.com/zalando/go-keyring"
)

const (
	keyringService = "pcloud-desktop"
	keyringUser    = "session"
)

// Le jeton (clé API, jamais le mot de passe) est stocké dans le
// trousseau système.
func saveSession(value string) {
	_ = keyring.Set(keyringService, keyringUser, value)
}

func loadSession() (value string, ok bool) {
	value, err := keyring.Get(keyringService, keyringUser)
	return value, err == nil && value != ""
}
