package ui

import (
	"strings"

	"github.com/zalando/go-keyring"
)

// Version est la version de l'application, renseignée par main.
var Version = "dev"

// VersionLabel retourne la version pour l'affichage : « v0.1.0 », ou « dev ».
func VersionLabel() string {
	if Version == "" || Version == "dev" {
		return "dev"
	}
	return "v" + strings.TrimPrefix(Version, "v")
}

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
