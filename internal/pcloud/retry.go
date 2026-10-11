package pcloud

import (
	"context"
	"errors"
	"time"
)

// Réglages par défaut des reprises automatiques.
const (
	defaultRetries    = 5
	defaultRetryDelay = time.Second
	maxRetryDelay     = 30 * time.Second
)

// statusError est une réponse HTTP inattendue d'un serveur de contenu.
type statusError struct {
	code int
	msg  string // message déjà traduit
}

func (e *statusError) Error() string { return e.msg }

// isTransient indique qu'une nouvelle tentative a une chance de réussir :
// coupure réseau, délai dépassé, réponse 5xx ou 429. Une erreur de l'API, un
// refus 4xx ou une annulation ne se réessaient pas.
func isTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.code >= 500 || se.code == 429 || se.code == 408
	}
	return true
}

// backoff attend avant la tentative suivante (1 s, 2 s, 4 s… plafonné), ou
// retourne l'erreur du contexte s'il est annulé entre-temps.
func (c *Client) backoff(ctx context.Context, attempt int) error {
	d := c.retryDelay
	for i := 0; i < attempt && d < maxRetryDelay; i++ {
		d *= 2
	}
	if d > maxRetryDelay {
		d = maxRetryDelay
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retry exécute fn jusqu'à c.retries échecs consécutifs transitoires. fn
// retourne l'erreur et si elle a fait avancer le transfert (ce qui remet le
// compteur à zéro : une longue transmission survit à des coupures répétées).
func (c *Client) retry(ctx context.Context, fn func() (progressed bool, err error)) error {
	for attempt := 0; ; {
		progressed, err := fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if !isTransient(err) {
			return err
		}
		if progressed {
			attempt = 0
		}
		if attempt >= c.retries {
			return err
		}
		if berr := c.backoff(ctx, attempt); berr != nil {
			return err
		}
		attempt++
	}
}
