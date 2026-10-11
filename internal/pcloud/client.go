// Package pcloud est un petit client pour l'API HTTP/JSON de pCloud.
// Documentation : https://docs.pcloud.com
package pcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultBase est le datacenter européen, seul utilisé par ce client.
const defaultBase = "https://eapi.pcloud.com"

// APIError est renvoyée quand pCloud répond avec un "result" différent de 0.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("pCloud (code %d) : %s", e.Code, e.Message)
}

// IsAuthError indique que le jeton est absent, invalide ou expiré : il faut
// se reconnecter.
func IsAuthError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case 1000, 2000, 2094, 2095:
		return true
	}
	return false
}

// Client parle à l'API pCloud. Il n'est pas prévu pour un usage concurrent
// pendant un Login ; ensuite, les appels sont sûrs.
type Client struct {
	base string
	auth string
	// authParam est le nom du paramètre portant le jeton : "auth" (défaut) ou
	// "access_token" pour un jeton OAuth.
	authParam string
	http      *http.Client // appels d'API, avec délai
	transfer  *http.Client // téléchargements et envois : durée bornée par le contexte
}

func New() *Client {
	return &Client{
		base:     defaultBase,
		http:     &http.Client{Timeout: 30 * time.Second},
		transfer: &http.Client{},
	}
}

// Auth retourne le jeton d'authentification courant (vide si non connecté).
func (c *Client) Auth() string { return c.auth }

// UseToken utilise une clé API (jeton "auth") déjà obtenue, et vérifie qu'elle
// est valide. En cas d'échec, le client reste déconnecté.
func (c *Client) UseToken(ctx context.Context, token string) error {
	return c.RestoreSession(ctx, token)
}

// Login se connecte avec l'e-mail et le mot de passe, et récupère un jeton.
func (c *Client) Login(ctx context.Context, user, pass string) error {
	c.auth, c.authParam = "", ""
	var out struct {
		Auth string `json:"auth"`
	}
	err := c.call(ctx, "userinfo", url.Values{
		"getauth":  {"1"},
		"username": {user},
		"password": {pass},
	}, &out)
	if err != nil {
		return err
	}
	c.auth = out.Auth
	return nil
}

// withAuth ajoute le jeton aux paramètres.
func (c *Client) withAuth(params url.Values) url.Values {
	if params == nil {
		params = url.Values{}
	}
	if c.auth != "" {
		name := c.authParam
		if name == "" {
			name = "auth"
		}
		params.Set(name, c.auth)
	}
	return params
}

// call exécute une méthode de l'API. Les paramètres partent en POST (corps du
// formulaire) pour que mot de passe et jeton ne figurent pas dans les URL.
func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	params = c.withAuth(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/"+method, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAPI(resp.Body, out)
}

// decodeAPI lit une réponse JSON de pCloud et la convertit en erreur si besoin.
func decodeAPI(r io.Reader, out any) error {
	var raw json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return fmt.Errorf("réponse illisible : %w", err)
	}
	var head struct {
		Result int    `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return err
	}
	if head.Result != 0 {
		return &APIError{Code: head.Result, Message: head.Error}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
