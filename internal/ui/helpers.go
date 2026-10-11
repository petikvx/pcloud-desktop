package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"github.com/zalando/go-keyring"
)

func iconFolder() fyne.Resource { return theme.FolderIcon() }
func iconFile() fyne.Resource   { return theme.FileIcon() }

func pathString(stack []pathEntry) string {
	if len(stack) == 1 {
		return "/"
	}
	parts := make([]string, 0, len(stack)-1)
	for _, e := range stack[1:] {
		parts = append(parts, e.name)
	}
	return "/" + strings.Join(parts, "/")
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d o", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %co", float64(n)/float64(div), "KMGTPE"[exp])
}

// La session (datacenter + jeton) est stockée dans le trousseau système,
// jamais le mot de passe.
func saveSession(host, token string) {
	_ = keyring.Set(keyringService, keyringUser, host+"|"+token)
}

func loadSession() (host, token string, ok bool) {
	v, err := keyring.Get(keyringService, keyringUser)
	if err != nil {
		return "", "", false
	}
	host, token, ok = strings.Cut(v, "|")
	return host, token, ok && host != "" && token != ""
}
