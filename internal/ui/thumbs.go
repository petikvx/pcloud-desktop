package ui

import (
	"sync"

	"fyne.io/fyne/v2"
)

// thumbCache garde les miniatures déjà chargées et limite les chargements
// simultanés. Une miniature en échec est mémorisée (nil) pour ne pas être
// redemandée en boucle.
type thumbCache struct {
	mu      sync.Mutex
	res     map[uint64]fyne.Resource
	pending map[uint64]bool
	sem     chan struct{}
}

func newThumbCache() *thumbCache {
	return &thumbCache{
		res:     map[uint64]fyne.Resource{},
		pending: map[uint64]bool{},
		sem:     make(chan struct{}, 4),
	}
}

// get retourne la miniature ; known vaut false si elle n'a pas encore été demandée.
func (t *thumbCache) get(id uint64) (res fyne.Resource, known bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	res, known = t.res[id]
	return res, known || t.pending[id]
}

// request lance le chargement (une seule fois par identifiant) puis appelle done.
func (t *thumbCache) request(id uint64, name string, fetch func() ([]byte, error), done func()) {
	t.mu.Lock()
	if _, ok := t.res[id]; ok || t.pending[id] {
		t.mu.Unlock()
		return
	}
	t.pending[id] = true
	t.mu.Unlock()

	go func() {
		t.sem <- struct{}{}
		data, err := fetch()
		<-t.sem

		t.mu.Lock()
		delete(t.pending, id)
		if err != nil || len(data) == 0 {
			t.res[id] = nil
		} else {
			t.res[id] = fyne.NewStaticResource(name, data)
		}
		t.mu.Unlock()
		if err == nil {
			done()
		}
	}()
}
