package ui

import (
	"reflect"
	"testing"
	"time"

	"pcloud-desktop/internal/pcloud"
)

func names(items []pcloud.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func sample() []pcloud.Item {
	return []pcloud.Item{
		{Name: "zeta.txt", Size: 10, Modified: "Mon, 02 Jan 2023 10:00:00 +0000"},
		{Name: "Alpha.txt", Size: 300, Modified: "Mon, 03 Jan 2022 10:00:00 +0000"},
		{Name: "docs", IsFolder: true, FolderID: 1, Modified: "Mon, 01 Jan 2024 10:00:00 +0000"},
		{Name: "beta.txt", Size: 20, Modified: "Mon, 04 Jan 2021 10:00:00 +0000"},
		{Name: "Archive", IsFolder: true, FolderID: 2},
	}
}

func TestSortByNameFoldersFirstCaseInsensitive(t *testing.T) {
	items := sample()
	sortItems(items, sortName, true)
	want := []string{"Archive", "docs", "Alpha.txt", "beta.txt", "zeta.txt"}
	if got := names(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	sortItems(items, sortName, false)
	want = []string{"docs", "Archive", "zeta.txt", "beta.txt", "Alpha.txt"}
	if got := names(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("desc : got %v, want %v", got, want)
	}
}

func TestSortBySizeAndDate(t *testing.T) {
	items := sample()
	sortItems(items, sortSize, false)
	if got := names(items)[2:]; !reflect.DeepEqual(got, []string{"Alpha.txt", "beta.txt", "zeta.txt"}) {
		t.Fatalf("taille desc : %v", got)
	}
	sortItems(items, sortDate, true)
	if got := names(items)[2:]; !reflect.DeepEqual(got, []string{"beta.txt", "Alpha.txt", "zeta.txt"}) {
		t.Fatalf("date asc : %v", got)
	}
	for _, it := range items[:2] {
		if !it.IsFolder {
			t.Fatal("les dossiers doivent rester en tête")
		}
	}
}

func TestFilterItemsDoesNotMutateInput(t *testing.T) {
	items := sample()
	got := filterItems(items, "  ALPHA ")
	if !reflect.DeepEqual(names(got), []string{"Alpha.txt"}) {
		t.Fatalf("got %v", names(got))
	}
	if len(items) != 5 {
		t.Fatal("l'entrée a été modifiée")
	}
	if len(filterItems(items, "")) != 5 {
		t.Fatal("un filtre vide doit tout garder")
	}
	if len(filterItems(items, "introuvable")) != 0 {
		t.Fatal("aucun résultat attendu")
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:               "0 o",
		1023:            "1023 o",
		1024:            "1.0 Ko",
		1536:            "1.5 Ko",
		5 * 1024 * 1024: "5.0 Mo",
		3 << 30:         "3.0 Go",
	}
	for n, want := range cases {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSummary(t *testing.T) {
	if got := summary(nil); got != "0 élément" {
		t.Fatalf("got %q", got)
	}
	items := []pcloud.Item{{Name: "a", Size: 1024}, {Name: "b", Size: 1024}, {Name: "d", IsFolder: true}}
	if got := summary(items); got != "3 éléments · 2.0 Ko" {
		t.Fatalf("got %q", got)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "mon fichier.txt", "..a"} {
		if !validName(ok) {
			t.Errorf("%q devrait être valide", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`} {
		if validName(bad) {
			t.Errorf("%q devrait être refusé", bad)
		}
	}
}

func TestFormatDateZero(t *testing.T) {
	if formatDate(time.Time{}) != "" {
		t.Fatal("date zéro : chaîne vide attendue")
	}
}
