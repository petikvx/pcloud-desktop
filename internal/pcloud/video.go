package pcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// flexString accepte une chaîne ou un nombre JSON (pCloud est incohérent sur
// certains champs comme fps et duration).
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" {
		s = ""
	}
	*f = flexString(s)
	return nil
}

// VideoVariant est une version d'une vidéo disponible en streaming :
// l'originale, ou une version transcodée par pCloud.
type VideoVariant struct {
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	Path         string     `json:"path"`
	Hosts        []string   `json:"hosts"`
	FPS          flexString `json:"fps"`
	Duration     flexString `json:"duration"`
	VideoCodec   string     `json:"videocodec"`
	AudioCodec   string     `json:"audiocodec"`
	VideoBitrate int        `json:"videobitrate"` // kb/s
	AudioBitrate int        `json:"audiobitrate"` // kb/s
	IsOriginal   bool       `json:"isoriginal"`
}

// DurationSeconds retourne la durée en secondes (0 si inconnue).
func (v VideoVariant) DurationSeconds() float64 {
	d, _ := strconv.ParseFloat(string(v.Duration), 64)
	return d
}

// VideoVariants liste les versions de la vidéo disponibles en streaming.
func (c *Client) VideoVariants(ctx context.Context, fileID uint64) ([]VideoVariant, error) {
	var out struct {
		Variants []VideoVariant `json:"variants"`
	}
	err := c.call(ctx, "getvideolinks", url.Values{
		"fileid":        {fmt.Sprint(fileID)},
		"forcedownload": {"1"},
	}, &out)
	return out.Variants, err
}

// VariantURL retourne l'URL de contenu d'une variante.
func (c *Client) VariantURL(v VideoVariant) (string, error) {
	return c.contentURL(v.Hosts, v.Path)
}

// VideoOptions décrit un transcodage personnalisé. Les champs à zéro gardent
// la valeur par défaut de pCloud.
type VideoOptions struct {
	Resolution   string // "1280x720" ; vide = résolution d'origine
	VideoBitrate int    // kb/s, 16 à 4000
	AudioBitrate int    // kb/s, 16 à 320
}

// VideoLink retourne l'URL d'une version transcodée à débit fixe (sans
// adaptation à la connexion, ce qui convient à un téléchargement).
func (c *Client) VideoLink(ctx context.Context, fileID uint64, opts VideoOptions) (string, error) {
	params := url.Values{
		"fileid":        {fmt.Sprint(fileID)},
		"fixedbitrate":  {"1"},
		"forcedownload": {"1"},
	}
	if opts.Resolution != "" {
		params.Set("resolution", opts.Resolution)
	}
	if opts.VideoBitrate > 0 {
		params.Set("vbitrate", fmt.Sprint(opts.VideoBitrate))
	}
	if opts.AudioBitrate > 0 {
		params.Set("abitrate", fmt.Sprint(opts.AudioBitrate))
	}
	var out struct {
		Hosts []string `json:"hosts"`
		Path  string   `json:"path"`
	}
	if err := c.call(ctx, "getvideolink", params, &out); err != nil {
		return "", err
	}
	return c.contentURL(out.Hosts, out.Path)
}

// UnexpectedResponseError signale une réponse valide (result 0) dont la forme
// n'est pas reconnue ; Raw contient le JSON reçu pour pouvoir le diagnostiquer.
type UnexpectedResponseError struct {
	Method string
	Raw    string
}

func (e *UnexpectedResponseError) Error() string {
	return fmt.Sprintf("réponse inattendue de %s : %s", e.Method, e.Raw)
}

// MediaTranscodeLink demande le lien de streaming transcodé d'une vidéo, avec
// la méthode (non documentée) qu'utilise le site web de pCloud. Le lien est
// souvent une playlist HLS (master.m3u8) ; l'appelant en décide d'après l'URL.
func (c *Client) MediaTranscodeLink(ctx context.Context, fileID uint64) (string, error) {
	var raw json.RawMessage
	err := c.call(ctx, "getmediatranscodelink", url.Values{
		"fileid":         {fmt.Sprint(fileID)},
		"transcodeahead": {"true"},
		"mediatype":      {"video"},
	}, &raw)
	if err != nil {
		return "", err
	}
	var r struct {
		Hosts []string `json:"hosts"`
		Path  string   `json:"path"`
		URL   string   `json:"url"`
		Link  string   `json:"link"`
	}
	if err := json.Unmarshal(raw, &r); err == nil {
		switch {
		case r.URL != "":
			return r.URL, nil
		case r.Link != "":
			return r.Link, nil
		case len(r.Hosts) > 0 && r.Path != "":
			return c.contentURL(r.Hosts, r.Path)
		}
	}
	s := string(raw)
	if len(s) > 1200 {
		s = s[:1200] + "…"
	}
	return "", &UnexpectedResponseError{Method: "getmediatranscodelink", Raw: s}
}
