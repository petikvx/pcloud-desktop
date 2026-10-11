// Package pcloud est un petit client pour l'API HTTP/JSON de pCloud.
// Documentation : https://docs.pcloud.com
package pcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	hostEU = "eapi.pcloud.com"
	hostUS = "api.pcloud.com"
)

// APIError est renvoyée quand pCloud répond avec un "result" différent de 0.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("pCloud (code %d) : %s", e.Code, e.Message)
}

// Item est un fichier ou un dossier.
type Item struct {
	Name     string `json:"name"`
	IsFolder bool   `json:"isfolder"`
	FolderID uint64 `json:"folderid"`
	FileID   uint64 `json:"fileid"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// Client parle à l'API pCloud. Il n'est pas prévu pour un usage concurrent
// pendant un Login ; ensuite, les appels de lecture sont sûrs.
type Client struct {
	host string
	auth string
	http *http.Client
}

func New() *Client {
	return &Client{
		host: hostEU,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Auth retourne le jeton d'authentification courant (vide si non connecté).
func (c *Client) Auth() string { return c.auth }

// Restore réutilise un jeton déjà obtenu, et vérifie qu'il est toujours valide.
func (c *Client) Restore(ctx context.Context, host, token string) error {
	c.host, c.auth = host, token
	var out struct{}
	return c.call(ctx, "userinfo", nil, &out)
}

// Host retourne le datacenter utilisé (EU ou US).
func (c *Client) Host() string { return c.host }

// Login essaie le datacenter européen puis américain : un compte n'existe que
// sur l'un des deux.
func (c *Client) Login(ctx context.Context, user, pass string) error {
	var lastErr error
	for _, host := range []string{hostEU, hostUS} {
		c.host, c.auth = host, ""
		var out struct {
			Auth string `json:"auth"`
		}
		err := c.call(ctx, "userinfo", url.Values{
			"getauth":  {"1"},
			"username": {user},
			"password": {pass},
		}, &out)
		if err == nil {
			c.auth = out.Auth
			return nil
		}
		lastErr = err
		var apiErr *APIError
		// 2000 = identifiants refusés (peut-être le mauvais datacenter) : on tente l'autre.
		if !errors.As(err, &apiErr) || apiErr.Code != 2000 {
			return err
		}
	}
	return lastErr
}

// ListFolder liste le contenu d'un dossier (0 = racine).
func (c *Client) ListFolder(ctx context.Context, folderID uint64) ([]Item, error) {
	var out struct {
		Metadata struct {
			Contents []Item `json:"contents"`
		} `json:"metadata"`
	}
	err := c.call(ctx, "listfolder", url.Values{
		"folderid": {fmt.Sprint(folderID)},
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Metadata.Contents, nil
}

// call exécute une méthode de l'API. Les paramètres partent en POST (corps du
// formulaire) pour que mot de passe et jeton ne figurent pas dans les URL.
func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	if c.auth != "" {
		params.Set("auth", c.auth)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+c.host+"/"+method, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
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
	return json.Unmarshal(raw, out)
}
