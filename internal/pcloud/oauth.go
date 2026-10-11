package pcloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authorizeURL = "https://my.pcloud.com/oauth2/authorize"
	// locationEU est le "locationid" renvoyé par pCloud pour le datacenter européen.
	locationEU = "2"
)

// OAuthConfig décrit l'application enregistrée sur pCloud (My apps).
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	// Port est le port local de la redirection http://localhost:<Port>/callback,
	// qui doit être déclarée telle quelle dans l'application pCloud.
	Port int
}

// RedirectURI est l'URL de redirection à enregistrer côté pCloud.
func (o OAuthConfig) RedirectURI() string {
	return fmt.Sprintf("http://localhost:%d/callback", o.Port)
}

// LoginOAuth connecte via le flux OAuth2 de pCloud (qui propose Google, Apple
// ou e-mail sur sa page). open reçoit l'URL à ouvrir dans le navigateur.
func (c *Client) LoginOAuth(ctx context.Context, cfg OAuthConfig, open func(authURL string) error) error {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return errors.New("client_id / client_secret pCloud manquants")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("port local %d indisponible : %w", cfg.Port, err)
	}
	defer ln.Close()

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	type result struct {
		code, location string
		err            error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			res.err = errors.New("réponse OAuth invalide (state)")
		case q.Get("error") != "":
			res.err = fmt.Errorf("autorisation refusée : %s", q.Get("error"))
		case q.Get("code") == "":
			res.err = errors.New("code d'autorisation absent")
		default:
			res.code, res.location = q.Get("code"), q.Get("locationid")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			fmt.Fprint(w, "<h3>Échec de la connexion.</h3>Vous pouvez fermer cet onglet.")
		} else {
			fmt.Fprint(w, "<h3>Connecté à pCloud.</h3>Vous pouvez fermer cet onglet et revenir à l'application.")
		}
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	authURL := authorizeURL + "?" + url.Values{
		"client_id":     {cfg.ClientID},
		"response_type": {"code"},
		"redirect_uri":  {cfg.RedirectURI()},
		"state":         {state},
	}.Encode()
	if err := open(authURL); err != nil {
		return fmt.Errorf("impossible d'ouvrir le navigateur : %w", err)
	}

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return errors.New("délai dépassé en attendant la connexion dans le navigateur")
	}
	if res.err != nil {
		return res.err
	}
	if res.location != "" && res.location != locationEU {
		return errors.New("ce compte est hébergé aux États-Unis ; seul eapi.pcloud.com (Europe) est géré")
	}

	// Échange du code contre un jeton.
	var out struct {
		AccessToken string `json:"access_token"`
	}
	err = c.call(ctx, "oauth2_token", url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {res.code},
	}, &out)
	if err != nil {
		return err
	}
	if out.AccessToken == "" {
		return errors.New("jeton absent de la réponse pCloud")
	}
	c.auth, c.authParam = out.AccessToken, "access_token"
	return nil
}

// SessionValue sérialise le jeton et son type pour le trousseau.
func (c *Client) SessionValue() string {
	if c.authParam == "access_token" {
		return "oauth:" + c.auth
	}
	return c.auth
}

// RestoreSession reprend une valeur produite par SessionValue et vérifie le jeton.
func (c *Client) RestoreSession(ctx context.Context, v string) error {
	if tok, ok := strings.CutPrefix(v, "oauth:"); ok {
		c.auth, c.authParam = tok, "access_token"
	} else {
		c.auth, c.authParam = v, ""
	}
	var out struct{}
	if err := c.call(ctx, "userinfo", nil, &out); err != nil {
		c.auth, c.authParam = "", ""
		return err
	}
	return nil
}
