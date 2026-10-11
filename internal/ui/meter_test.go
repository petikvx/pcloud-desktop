package ui

import (
	"sync"
	"testing"
	"time"
)

func fakeClock() (*time.Time, func() time.Time) {
	t := time.Unix(1_700_000_000, 0)
	return &t, func() time.Time { return t }
}

func TestSpeedMeterRateAndWindow(t *testing.T) {
	now, clock := fakeClock()
	m := newSpeedMeter()
	m.now = clock
	m.samples = []meterSample{{t: clock()}}

	if m.Rate() != 0 {
		t.Fatal("mesure trop courte : débit nul attendu")
	}
	// 1 Mo/s pendant 2 s.
	*now = now.Add(2 * time.Second)
	m.Add(2_000_000)
	if r := m.Rate(); r < 990_000 || r > 1_010_000 {
		t.Fatalf("débit = %v, attendu ~1 Mo/s", r)
	}
	// Le débit monte à 4 Mo/s : la fenêtre glissante finit par le refléter.
	for i := 0; i < 10; i++ {
		*now = now.Add(time.Second)
		m.Add(4_000_000)
		m.Rate()
	}
	if r := m.Rate(); r < 3_800_000 || r > 4_200_000 {
		t.Fatalf("débit lissé = %v, attendu ~4 Mo/s", r)
	}
	if m.Total() != 42_000_000 {
		t.Fatalf("total = %d", m.Total())
	}
}

func TestSpeedMeterConcurrentAdd(t *testing.T) {
	m := newSpeedMeter()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				m.Add(10)
			}
		}()
	}
	wg.Wait()
	if m.Total() != 80_000 {
		t.Fatalf("total = %d", m.Total())
	}
}

func TestFormatETAAndRate(t *testing.T) {
	cases := map[float64]string{0: "", 0.4: "", 45: "~45 s", 90: "~2 min", 3725: "~1 h 02"}
	for in, want := range cases {
		if got := formatETA(in); got != want {
			t.Errorf("formatETA(%v) = %q, want %q", in, got, want)
		}
	}
	if formatRate(0) != "" || formatRate(5*1024*1024) != "5.0 Mo/s" {
		t.Errorf("formatRate : %q / %q", formatRate(0), formatRate(5*1024*1024))
	}
}

func TestProgressText(t *testing.T) {
	got := progressText("Film.mp4", 100<<20, 400<<20, 10<<20)
	want := "Film.mp4 · 100.0 Mo / 400.0 Mo · 10.0 Mo/s · reste ~30 s"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := progressText("x", 1024, 0, 0); got != "x · 1.0 Ko" {
		t.Fatalf("taille inconnue : %q", got)
	}
}

func TestHLSProgressText(t *testing.T) {
	// 10 segments, 4 reçus pour 40 Mo : il en reste 6 d'environ 10 Mo à 10 Mo/s → ~6 s.
	got := hlsProgressText("Vidéo", 4, 10, 40<<20, 40<<20, 10<<20)
	want := "Vidéo · segment 4/10 · 40.0 Mo · 10.0 Mo/s · reste ~6 s"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := hlsProgressText("Audio", 1, 5, 1024, 1024, 0); got != "Audio · segment 1/5 · 1.0 Ko" {
		t.Fatalf("sans débit : %q", got)
	}
}
