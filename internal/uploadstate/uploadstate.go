// Package uploadstate mémorise sur disque les envois en cours (identifiant
// d'envoi pCloud par fichier local) pour pouvoir les reprendre après un arrêt
// de l'application.
package uploadstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxAge : au-delà, un envoi mémorisé est considéré expiré côté serveur.
const maxAge = 3 * 24 * time.Hour

type entry struct {
	ID      uint64    `json:"id"`
	Created time.Time `json:"created"`
}

// Store est un petit fichier JSON clé → identifiant d'envoi. Sûr en concurrence.
type Store struct {
	path string
	now  func() time.Time

	mu sync.Mutex
	m  map[string]entry
}

// DefaultPath est l'emplacement du fichier dans le dossier de cache de
// l'utilisateur. Vide si ce dossier est introuvable (le suivi est alors
// seulement en mémoire).
func DefaultPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "pcloud-desktop", "uploads.json")
}

// Open charge le fichier (absent ou illisible : magasin vide) et retire les
// entrées expirées. path vide : mémoire seulement.
func Open(path string) *Store {
	s := &Store{path: path, now: time.Now, m: map[string]entry{}}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &s.m)
		}
	}
	s.prune()
	return s
}

func (s *Store) prune() {
	for k, e := range s.m {
		if s.now().Sub(e.Created) > maxAge {
			delete(s.m, k)
		}
	}
}

// Key identifie un envoi : le même fichier local (chemin, taille, date), vers
// le même dossier et le même nom.
func Key(localPath string, size int64, mtime time.Time, folderID uint64, name string) string {
	return fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%s", localPath, size, mtime.UnixNano(), folderID, name)
}

// For retourne la vue du magasin pour une clé ; elle satisfait pcloud.ResumeStore.
func (s *Store) For(key string) *Handle { return &Handle{s: s, key: key} }

// Handle donne accès à l'entrée d'une clé.
type Handle struct {
	s   *Store
	key string
}

// Get retourne l'identifiant mémorisé, ou 0.
func (h *Handle) Get() uint64 {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	h.s.prune()
	return h.s.m[h.key].ID
}

// Put mémorise l'identifiant.
func (h *Handle) Put(id uint64) {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	h.s.m[h.key] = entry{ID: id, Created: h.s.now()}
	h.s.save()
}

// Clear oublie l'identifiant.
func (h *Handle) Clear() {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	if _, ok := h.s.m[h.key]; ok {
		delete(h.s.m, h.key)
		h.s.save()
	}
}

// save écrit le fichier (écriture atomique). Une erreur est sans gravité : au
// pire l'envoi ne sera pas repris après un redémarrage.
func (s *Store) save() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(s.m)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}
