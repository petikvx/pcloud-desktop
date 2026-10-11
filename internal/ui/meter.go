package ui

import (
	"fmt"
	"sync"
	"time"
)

// meterWindow : durée sur laquelle le débit est lissé.
const meterWindow = 4 * time.Second

type meterSample struct {
	t     time.Time
	total int64
}

// speedMeter mesure un débit moyen sur une fenêtre glissante. Sûr en
// concurrence : Add peut être appelé depuis plusieurs goroutines.
type speedMeter struct {
	mu      sync.Mutex
	now     func() time.Time // remplaçable dans les tests
	total   int64
	started bool // un cumul a déjà été fixé par Set
	samples []meterSample
	rate    float64
}

func newSpeedMeter() *speedMeter {
	m := &speedMeter{now: time.Now}
	m.samples = []meterSample{{t: m.now()}}
	return m
}

// Add ajoute n octets reçus.
func (m *speedMeter) Add(n int64) {
	m.mu.Lock()
	m.total += n
	m.mu.Unlock()
}

// Set fixe le total cumulé (quand la source donne un cumul, pas un incrément).
// Le premier cumul sert de point de départ : un transfert repris à l'octet N ne
// compte pas ces N octets dans le débit.
func (m *speedMeter) Set(total int64) {
	m.mu.Lock()
	if !m.started {
		m.started = true
		m.samples[0].total = total
	}
	m.total = total
	m.mu.Unlock()
}

// Total retourne le cumul d'octets.
func (m *speedMeter) Total() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total
}

// Rate retourne le débit en octets par seconde (0 tant que la mesure est trop
// courte). À appeler régulièrement : chaque appel ajoute un point de mesure.
func (m *speedMeter) Rate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.samples = append(m.samples, meterSample{t: now, total: m.total})
	cutoff := now.Add(-meterWindow)
	for len(m.samples) > 1 && !m.samples[1].t.After(cutoff) {
		m.samples = m.samples[1:]
	}
	oldest := m.samples[0]
	if dt := now.Sub(oldest.t); dt >= 500*time.Millisecond {
		m.rate = float64(m.total-oldest.total) / dt.Seconds()
	}
	return m.rate
}

// formatRate : « 5.1 Mo/s ».
func formatRate(bps float64) string {
	if bps <= 0 {
		return ""
	}
	return humanSize(int64(bps)) + "/s"
}

// formatETA : « ~45 s », « ~3 min », « ~1 h 05 ».
func formatETA(sec float64) string {
	switch {
	case sec < 1:
		return ""
	case sec < 60:
		return fmt.Sprintf("~%d s", int(sec+0.5))
	case sec < 3600:
		return fmt.Sprintf("~%d min", int(sec/60+0.5))
	}
	h := int(sec / 3600)
	return fmt.Sprintf("~%d h %02d", h, int(sec-float64(h)*3600)/60)
}

// progressText compose « nom · 120 Mo / 2.2 Go · 5.1 Mo/s · reste ~3 min ».
// total <= 0 : taille inconnue (pas de temps restant).
func progressText(label string, done, total int64, rate float64) string {
	s := label + " · " + humanSize(done)
	if total > 0 {
		s += " / " + humanSize(total)
	}
	if r := formatRate(rate); r != "" {
		s += " · " + r
		if total > done {
			if eta := formatETA(float64(total-done) / rate); eta != "" {
				s += trf(" · reste %s", eta)
			}
		}
	}
	return s
}
