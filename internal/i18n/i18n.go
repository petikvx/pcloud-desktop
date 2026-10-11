// Package i18n traduit les textes de l'interface. Les textes sources sont en
// français et servent de clé : une chaîne sans traduction est affichée telle
// quelle, en français.
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// Langues proposées.
const (
	FR = "fr"
	EN = "en"
)

// Language décrit une langue proposée, avec son nom dans cette langue.
type Language struct {
	Code string
	Name string
}

// Languages liste les langues disponibles.
func Languages() []Language {
	return []Language{{FR, "Français"}, {EN, "English"}}
}

var current atomic.Value // string

func init() { current.Store(FR) }

// Normalize ramène un code de langue (« fr_FR.UTF-8 », « en-US »…) à une
// langue disponible. Retourne "" si la langue n'est pas gérée.
func Normalize(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, l := range Languages() {
		if code == l.Code || strings.HasPrefix(code, l.Code+"_") ||
			strings.HasPrefix(code, l.Code+"-") || strings.HasPrefix(code, l.Code+".") {
			return l.Code
		}
	}
	return ""
}

// Set choisit la langue ; un code inconnu donne le français.
func Set(code string) {
	if l := Normalize(code); l != "" {
		current.Store(l)
		return
	}
	current.Store(FR)
}

// Current retourne le code de la langue active.
func Current() string { return current.Load().(string) }

// Detect devine la langue de l'utilisateur d'après l'environnement
// (LC_ALL, LC_MESSAGES, LANGUAGE, LANG). Sans indication reconnue : anglais.
func Detect() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		// LANGUAGE peut être une liste séparée par « : ».
		for _, c := range strings.Split(v, ":") {
			if l := Normalize(c); l != "" {
				return l
			}
			if c == "C" || c == "POSIX" {
				return EN
			}
		}
	}
	return EN
}

// T retourne la traduction de s dans la langue active.
func T(s string) string {
	if Current() == EN {
		if t, ok := en[s]; ok {
			return t
		}
	}
	return s
}

// Tf traduit format puis le complète avec args (fmt.Sprintf).
func Tf(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// N choisit entre le singulier et le pluriel selon n et la langue, traduit le
// résultat et y insère n : N(2, "%d fichier", "%d fichiers").
// En français 0 et 1 sont au singulier ; en anglais, seul 1 l'est.
func N(n int, one, other string) string {
	singular := n == 1
	if Current() == FR {
		singular = n >= 0 && n <= 1
	}
	if singular {
		return Tf(one, n)
	}
	return Tf(other, n)
}
