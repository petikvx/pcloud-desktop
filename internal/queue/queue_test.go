package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func tasks(n int) []Task {
	ts := make([]Task, n)
	for i := range ts {
		ts[i].Name = string(rune('a' + i))
	}
	return ts
}

func TestRunSequentialOutcomes(t *testing.T) {
	var order []int
	boom := errors.New("boom")
	sum := Run(context.Background(), tasks(4), func(_ context.Context, i int, report func(string, float64)) error {
		order = append(order, i)
		report("en cours", 0.5)
		switch i {
		case 1:
			return boom
		case 2:
			return ErrSkipped
		}
		return nil
	}, nil)
	if sum != (Summary{Done: 2, Failed: 1, Skipped: 1}) {
		t.Fatalf("résumé = %+v", sum)
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("ordre = %v", order)
		}
	}
}

func TestRunKeepsGoingAfterFailureAndReportsSnapshots(t *testing.T) {
	var mu sync.Mutex
	var last []Task
	Run(context.Background(), tasks(2), func(_ context.Context, i int, _ func(string, float64)) error {
		if i == 0 {
			return errors.New("échec")
		}
		return nil
	}, func(ts []Task) {
		mu.Lock()
		last = ts
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	if last[0].State != Failed || last[0].Err == nil || last[1].State != Done || last[1].Frac != 1 {
		t.Fatalf("état final = %+v", last)
	}
}

func TestRunCancelMarksRemaining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ran int
	sum := Run(ctx, tasks(5), func(ctx context.Context, i int, _ func(string, float64)) error {
		ran++
		if i == 1 {
			cancel()
			return ctx.Err()
		}
		return nil
	}, nil)
	if ran != 2 {
		t.Fatalf("tâches exécutées = %d", ran)
	}
	if sum.Done != 1 || sum.Canceled != 4 || sum.Failed != 0 {
		t.Fatalf("résumé = %+v", sum)
	}
}

func TestRunThrottlesProgressNotifications(t *testing.T) {
	var n int
	Run(context.Background(), tasks(1), func(_ context.Context, _ int, report func(string, float64)) error {
		for i := 0; i < 1000; i++ {
			report("x", float64(i)/1000)
		}
		time.Sleep(10 * time.Millisecond)
		return nil
	}, func([]Task) { n++ })
	if n > 20 {
		t.Fatalf("%d notifications : la progression doit être limitée", n)
	}
	if n < 2 {
		t.Fatalf("%d notifications : au moins début et fin attendus", n)
	}
}
