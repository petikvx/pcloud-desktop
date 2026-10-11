// Package hls lit les playlists HLS (.m3u8) et télécharge leurs segments,
// sans dépendance externe (en remplacement de yt-dlp pour les flux simples).
//
// Gère : playlist maître (variantes, pistes audio/sous-titres avec langue),
// playlist média (segments, byte ranges, segment d'initialisation fMP4) et
// chiffrement AES-128. Ne gère pas SAMPLE-AES ni les flux en direct.
package hls

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// Variant est une qualité vidéo proposée par la playlist maître.
type Variant struct {
	URI          string // absolue
	Bandwidth    int    // bit/s
	AvgBandwidth int
	Width        int
	Height       int
	FrameRate    float64
	Codecs       string
	Audio        string // groupe de pistes audio associé
	Subtitles    string // groupe de sous-titres associé
}

// Rendition est une piste alternative : audio, sous-titres…
type Rendition struct {
	Type       string // AUDIO, SUBTITLES, VIDEO, CLOSED-CAPTIONS
	GroupID    string
	Name       string
	Language   string
	URI        string // absolue ; vide si la piste est déjà dans le flux de la variante
	Default    bool
	AutoSelect bool
	Forced     bool
	Channels   string
}

// Master est une playlist maître.
type Master struct {
	Variants   []Variant
	Renditions []Rendition
}

// AudioFor retourne les pistes audio du groupe de la variante.
func (m *Master) AudioFor(v Variant) []Rendition { return m.group("AUDIO", v.Audio) }

// SubtitlesFor retourne les sous-titres du groupe de la variante.
func (m *Master) SubtitlesFor(v Variant) []Rendition { return m.group("SUBTITLES", v.Subtitles) }

// AllRenditions retourne toutes les pistes d'un type (AUDIO, SUBTITLES…) tous
// groupes confondus, sans doublon d'URI. Un master peut déclarer plusieurs
// versions d'une même langue (par exemple un groupe par qualité audio) : c'est
// ce qui permet de choisir l'audio indépendamment de la vidéo.
func (m *Master) AllRenditions(typ string) []Rendition {
	seen := map[string]bool{}
	var out []Rendition
	for _, r := range m.Renditions {
		if r.Type != typ || r.URI == "" || seen[r.URI] {
			continue
		}
		seen[r.URI] = true
		out = append(out, r)
	}
	return out
}

func (m *Master) group(typ, id string) []Rendition {
	if id == "" {
		return nil
	}
	var out []Rendition
	for _, r := range m.Renditions {
		if r.Type == typ && r.GroupID == id {
			out = append(out, r)
		}
	}
	return out
}

// Key décrit le chiffrement d'un segment.
type Key struct {
	Method string // NONE ou AES-128
	URI    string // absolue
	IV     []byte // nil : dérivé du numéro de séquence
}

// Segment est un morceau de média.
type Segment struct {
	URI      string
	Duration float64
	Offset   int64 // octet de départ (BYTERANGE)
	Length   int64 // 0 : le fichier entier
	Seq      int64 // numéro de séquence média
	Key      *Key  // nil : en clair
}

// Media est une playlist média.
type Media struct {
	Segments       []Segment
	Init           *Segment // EXT-X-MAP : segment d'initialisation (fMP4)
	TargetDuration float64
	Duration       float64
	EndList        bool // false : flux en direct
}

// Parse lit une playlist. Exactement un des deux résultats est non nil.
// base sert à résoudre les URI relatives.
func Parse(r io.Reader, base *url.URL) (*Master, *Media, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var lines []string
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "#EXTM3U") {
		return nil, nil, errors.New("ce n'est pas une playlist HLS (#EXTM3U absent)")
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF:") {
			m, err := parseMaster(lines, base)
			return m, nil, err
		}
	}
	m, err := parseMedia(lines, base)
	return nil, m, err
}

func resolve(base *url.URL, ref string) (string, error) {
	if base == nil {
		return ref, nil
	}
	u, err := base.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("URI invalide %q : %w", ref, err)
	}
	return u.String(), nil
}

func parseMaster(lines []string, base *url.URL) (*Master, error) {
	m := &Master{}
	var pending map[string]string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "#EXT-X-STREAM-INF:"):
			pending = parseAttrs(strings.TrimPrefix(l, "#EXT-X-STREAM-INF:"))
		case strings.HasPrefix(l, "#EXT-X-MEDIA:"):
			a := parseAttrs(strings.TrimPrefix(l, "#EXT-X-MEDIA:"))
			r := Rendition{
				Type: a["TYPE"], GroupID: a["GROUP-ID"], Name: a["NAME"], Language: a["LANGUAGE"],
				Default: a["DEFAULT"] == "YES", AutoSelect: a["AUTOSELECT"] == "YES",
				Forced: a["FORCED"] == "YES", Channels: a["CHANNELS"],
			}
			if uri := a["URI"]; uri != "" {
				abs, err := resolve(base, uri)
				if err != nil {
					return nil, err
				}
				r.URI = abs
			}
			m.Renditions = append(m.Renditions, r)
		case strings.HasPrefix(l, "#"):
			// autre balise : ignorée
		default:
			if pending == nil {
				continue
			}
			abs, err := resolve(base, l)
			if err != nil {
				return nil, err
			}
			v := Variant{URI: abs, Codecs: pending["CODECS"], Audio: pending["AUDIO"], Subtitles: pending["SUBTITLES"]}
			v.Bandwidth, _ = strconv.Atoi(pending["BANDWIDTH"])
			v.AvgBandwidth, _ = strconv.Atoi(pending["AVERAGE-BANDWIDTH"])
			if w, h, ok := strings.Cut(pending["RESOLUTION"], "x"); ok {
				v.Width, _ = strconv.Atoi(w)
				v.Height, _ = strconv.Atoi(h)
			}
			v.FrameRate, _ = strconv.ParseFloat(pending["FRAME-RATE"], 64)
			m.Variants = append(m.Variants, v)
			pending = nil
		}
	}
	if len(m.Variants) == 0 {
		return nil, errors.New("playlist maître sans variante")
	}
	return m, nil
}

func parseMedia(lines []string, base *url.URL) (*Media, error) {
	m := &Media{}
	var (
		seq      int64
		dur      float64
		key      *Key
		length   int64 // BYTERANGE en attente pour le prochain segment
		rangeOff int64
		hasOff   bool // l'offset est explicite (« n@o »)
		hasRange bool
		nextOff  int64 // fin du dernier byte range : offset implicite suivant
		lastURI  string
	)
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.ParseInt(strings.TrimPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(l, "#EXT-X-TARGETDURATION:"):
			m.TargetDuration, _ = strconv.ParseFloat(strings.TrimPrefix(l, "#EXT-X-TARGETDURATION:"), 64)
		case l == "#EXT-X-ENDLIST":
			m.EndList = true
		case strings.HasPrefix(l, "#EXTINF:"):
			s := strings.TrimPrefix(l, "#EXTINF:")
			s, _, _ = strings.Cut(s, ",")
			dur, _ = strconv.ParseFloat(strings.TrimSpace(s), 64)
		case strings.HasPrefix(l, "#EXT-X-BYTERANGE:"):
			n, o, explicit := strings.Cut(strings.TrimPrefix(l, "#EXT-X-BYTERANGE:"), "@")
			length, _ = strconv.ParseInt(n, 10, 64)
			rangeOff, hasOff = 0, explicit
			if explicit {
				rangeOff, _ = strconv.ParseInt(o, 10, 64)
			}
			hasRange = true
		case strings.HasPrefix(l, "#EXT-X-KEY:"):
			a := parseAttrs(strings.TrimPrefix(l, "#EXT-X-KEY:"))
			switch a["METHOD"] {
			case "NONE", "":
				key = nil
			case "AES-128":
				abs, err := resolve(base, a["URI"])
				if err != nil {
					return nil, err
				}
				k := &Key{Method: "AES-128", URI: abs}
				if iv := a["IV"]; iv != "" {
					b, err := parseHex(iv)
					if err != nil || len(b) != 16 {
						return nil, fmt.Errorf("IV invalide : %q", iv)
					}
					k.IV = b
				}
				key = k
			default:
				return nil, fmt.Errorf("chiffrement %s non géré (seul AES-128 l'est)", a["METHOD"])
			}
		case strings.HasPrefix(l, "#EXT-X-MAP:"):
			a := parseAttrs(strings.TrimPrefix(l, "#EXT-X-MAP:"))
			abs, err := resolve(base, a["URI"])
			if err != nil {
				return nil, err
			}
			init := &Segment{URI: abs}
			if br := a["BYTERANGE"]; br != "" {
				n, o, _ := strings.Cut(br, "@")
				init.Length, _ = strconv.ParseInt(n, 10, 64)
				init.Offset, _ = strconv.ParseInt(o, 10, 64)
			}
			m.Init = init
		case strings.HasPrefix(l, "#"):
			// autre balise : ignorée
		default:
			abs, err := resolve(base, l)
			if err != nil {
				return nil, err
			}
			s := Segment{URI: abs, Duration: dur, Seq: seq, Key: key}
			if hasRange {
				s.Length = length
				switch {
				case hasOff:
					s.Offset = rangeOff
				case abs == lastURI:
					s.Offset = nextOff
				}
				nextOff = s.Offset + length
			}
			lastURI = abs
			hasRange, hasOff = false, false
			m.Segments = append(m.Segments, s)
			m.Duration += dur
			seq++
			dur = 0
		}
	}
	if len(m.Segments) == 0 {
		return nil, errors.New("playlist média sans segment")
	}
	return m, nil
}

// parseAttrs lit une liste d'attributs « CLE=valeur,CLE="valeur, avec virgule" ».
func parseAttrs(s string) map[string]string {
	m := map[string]string{}
	for len(s) > 0 {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				val, s = s[1:], ""
			} else {
				val, s = s[1:1+end], s[end+2:]
			}
			s = strings.TrimPrefix(s, ",")
		} else if comma := strings.IndexByte(s, ','); comma < 0 {
			val, s = s, ""
		} else {
			val, s = s[:comma], s[comma+1:]
		}
		m[key] = val
	}
	return m
}

func parseHex(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		n, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(n)
	}
	return out, nil
}
