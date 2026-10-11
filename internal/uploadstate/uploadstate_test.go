package uploadstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistsAcrossOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "uploads.json")
	k := Key("/a/b.bin", 10, time.Unix(5, 0), 3, "b.bin")

	s := Open(p)
	if got := s.For(k).Get(); got != 0 {
		t.Fatalf("Get = %d", got)
	}
	s.For(k).Put(77)

	s2 := Open(p)
	if got := s2.For(k).Get(); got != 77 {
		t.Fatalf("Get après réouverture = %d", got)
	}
	other := Key("/a/b.bin", 11, time.Unix(5, 0), 3, "b.bin")
	if got := s2.For(other).Get(); got != 0 {
		t.Fatalf("une autre taille ne doit pas retrouver l'envoi : %d", got)
	}
	s2.For(k).Clear()
	if got := Open(p).For(k).Get(); got != 0 {
		t.Fatalf("Get après Clear = %d", got)
	}
}

func TestExpiredEntriesDropped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "u.json")
	s := Open(p)
	s.For("k").Put(9)

	later := Open(p)
	later.now = func() time.Time { return time.Now().Add(maxAge + time.Hour) }
	if got := later.For("k").Get(); got != 0 {
		t.Fatalf("entrée expirée toujours là : %d", got)
	}
}

func TestMemoryOnlyAndCorruptFile(t *testing.T) {
	s := Open("")
	s.For("k").Put(1)
	if s.For("k").Get() != 1 {
		t.Fatal("mémoire seule : valeur perdue")
	}
	p := filepath.Join(t.TempDir(), "bad.json")
	_ = writeFile(p, "{pas du json")
	if Open(p).For("k").Get() != 0 {
		t.Fatal("fichier corrompu : magasin vide attendu")
	}
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o600) }
