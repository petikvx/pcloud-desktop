// Package queue exécute des tâches l'une après l'autre en suivant leur état :
// la file d'attente des téléchargements d'un dossier.
package queue

import (
	"context"
	"errors"
	"sync"
	"time"

	"pcloud-desktop/internal/i18n"
)

// State est l'état d'une tâche.
type State int

const (
	Pending State = iota
	Running
	Done
	Failed
	Skipped
	Canceled
)

// String retourne l'état, traduit dans la langue active.
func (s State) String() string {
	switch s {
	case Pending:
		return i18n.T("En attente")
	case Running:
		return i18n.T("En cours")
	case Done:
		return i18n.T("Terminé")
	case Failed:
		return i18n.T("Échec")
	case Skipped:
		return i18n.T("Ignoré")
	case Canceled:
		return i18n.T("Annulé")
	}
	return ""
}

// ErrSkipped : la tâche n'avait rien à faire (fichier déjà présent…).
var ErrSkipped = errors.New("ignoré")

// Task est une tâche suivie.
type Task struct {
	Name  string
	State State
	Err   error
	Frac  float64 // 0..1
	Text  string  // détail de progression
}

// Work exécute la tâche i. report met à jour le texte et l'avancement.
type Work func(ctx context.Context, i int, report func(text string, frac float64)) error

// Summary compte les tâches par issue.
type Summary struct {
	Done, Failed, Skipped, Canceled int
}

// Run exécute les tâches dans l'ordre. Un échec n'arrête pas la file ;
// l'annulation du contexte marque le reste comme annulé. onChange reçoit une
// copie de l'état (au plus ~10 fois par seconde pour la progression) et ne doit
// pas rappeler Run.
func Run(ctx context.Context, tasks []Task, work Work, onChange func([]Task)) Summary {
	var mu sync.Mutex
	ts := append([]Task(nil), tasks...)
	var last time.Time
	notify := func(force bool) { // mu tenu
		if onChange == nil {
			return
		}
		if now := time.Now(); force || now.Sub(last) >= 100*time.Millisecond {
			last = now
			onChange(append([]Task(nil), ts...))
		}
	}

	for i := range ts {
		if ctx.Err() != nil {
			mu.Lock()
			for j := i; j < len(ts); j++ {
				ts[j].State = Canceled
			}
			notify(true)
			mu.Unlock()
			break
		}
		mu.Lock()
		ts[i].State = Running
		notify(true)
		mu.Unlock()

		err := work(ctx, i, func(text string, frac float64) {
			mu.Lock()
			ts[i].Text, ts[i].Frac = text, frac
			notify(false)
			mu.Unlock()
		})

		mu.Lock()
		switch {
		case err == nil:
			ts[i].State, ts[i].Frac = Done, 1
		case errors.Is(err, ErrSkipped):
			ts[i].State = Skipped
		case ctx.Err() != nil:
			ts[i].State = Canceled
		default:
			ts[i].State, ts[i].Err = Failed, err
		}
		notify(true)
		mu.Unlock()
	}

	var s Summary
	mu.Lock()
	defer mu.Unlock()
	for _, t := range ts {
		switch t.State {
		case Done:
			s.Done++
		case Failed:
			s.Failed++
		case Skipped:
			s.Skipped++
		case Canceled:
			s.Canceled++
		}
	}
	return s
}
