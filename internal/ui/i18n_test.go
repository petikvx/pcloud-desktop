package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"pcloud-desktop/internal/i18n"
	"pcloud-desktop/internal/pcloud"
)

// texts collecte les textes des boutons, libellés et champs d'un arbre d'objets.
func texts(o fyne.CanvasObject, out *[]string) {
	switch v := o.(type) {
	case *widget.Button:
		*out = append(*out, v.Text)
	case *widget.Label:
		*out = append(*out, v.Text)
	case *widget.Entry:
		*out = append(*out, v.PlaceHolder)
	}
	switch v := o.(type) {
	case *fyne.Container:
		for _, c := range v.Objects {
			texts(c, out)
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			texts(c, out)
		}
	}
}

func screenTexts(w fyne.Window) string {
	var out []string
	texts(w.Content(), &out)
	return strings.Join(out, "|")
}

// Le choix d'une langue dans le menu change la langue active, la mémorise, et
// l'écran reconstruit est entièrement dans la nouvelle langue. Le chargement du
// dossier (réseau) n'est pas lancé : seul l'écran est vérifié.
func TestBrowserSwitchesLanguage(t *testing.T) {
	defer i18n.Set(i18n.FR)
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("test")

	i18n.Set(i18n.FR)
	b := newBrowser(a, w, pcloud.New())
	b.build()
	if got := screenTexts(w); !strings.Contains(got, "Envoyer") || !strings.Contains(got, "Nouveau dossier") {
		t.Fatalf("écran français : %q", got)
	}

	var english *fyne.MenuItem
	items := languageMenu(a, func() { b.release(); b.build() })
	for _, it := range items {
		if it.Label == "English" {
			english = it
		}
		if (it.Label == "Français") != it.Checked {
			t.Fatalf("« %s » : coche = %v", it.Label, it.Checked)
		}
	}
	if english == nil {
		t.Fatal("entrée English absente du menu")
	}
	english.Action()

	if i18n.Current() != i18n.EN {
		t.Fatalf("langue active = %q", i18n.Current())
	}
	if got := a.Preferences().String(prefLang); got != "en" {
		t.Fatalf("préférence = %q", got)
	}
	got := screenTexts(w)
	if !strings.Contains(got, "Upload") || !strings.Contains(got, "New folder") || !strings.Contains(got, "Sign out") {
		t.Fatalf("écran anglais : %q", got)
	}
	if strings.Contains(got, "Envoyer") || strings.Contains(got, "Nouveau dossier") || strings.Contains(got, "Déconnexion") {
		t.Fatalf("des textes français restent : %q", got)
	}
	b.release()
}

func TestLoginScreenInBothLanguages(t *testing.T) {
	defer i18n.Set(i18n.FR)
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("test")

	i18n.Set(i18n.FR)
	showLogin(a, w, pcloud.New(), "")
	if got := screenTexts(w); !strings.Contains(got, "Se connecter") {
		t.Fatalf("écran français : %q", got)
	}
	i18n.Set(i18n.EN)
	showLogin(a, w, pcloud.New(), "")
	if got := screenTexts(w); !strings.Contains(got, "Sign in") || strings.Contains(got, "Se connecter") {
		t.Fatalf("écran anglais : %q", got)
	}
}

func TestInitLanguageUsesPreferenceThenEnvironment(t *testing.T) {
	defer i18n.Set(i18n.FR)
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE"} {
		t.Setenv(k, "")
	}
	t.Setenv("LANG", "fr_FR.UTF-8")
	a := test.NewApp()
	defer a.Quit()

	InitLanguage(a)
	if i18n.Current() != i18n.FR {
		t.Fatalf("langue = %q, attendu fr (environnement)", i18n.Current())
	}
	a.Preferences().SetString(prefLang, "en")
	InitLanguage(a)
	if i18n.Current() != i18n.EN {
		t.Fatalf("langue = %q, attendu en (préférence)", i18n.Current())
	}
}

func TestFormatsFollowLanguage(t *testing.T) {
	defer i18n.Set(i18n.FR)
	i18n.Set(i18n.FR)
	if got := humanSize(1536); got != "1.5 Ko" {
		t.Fatalf("fr : %q", got)
	}
	if got := summary([]pcloud.Item{{Name: "a"}}); got != "1 élément" {
		t.Fatalf("fr : %q", got)
	}
	i18n.Set(i18n.EN)
	if got := humanSize(1536); got != "1.5 KB" {
		t.Fatalf("en : %q", got)
	}
	if got := humanSize(12); got != "12 B" {
		t.Fatalf("en : %q", got)
	}
	if got := summary([]pcloud.Item{{Name: "a"}, {Name: "b"}}); got != "2 items" {
		t.Fatalf("en : %q", got)
	}
	if got := progressText("f", 1<<20, 4<<20, 1<<20); !strings.Contains(got, "left") || !strings.Contains(got, "MB/s") {
		t.Fatalf("en : %q", got)
	}
}
