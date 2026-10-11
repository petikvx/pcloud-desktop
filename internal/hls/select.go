package hls

import (
	"errors"
	"sort"
	"strings"

	"pcloud-desktop/internal/i18n"
)

// Quality désigne une version d'une piste quand une même langue en propose
// plusieurs (par exemple « low », « normal », « high »).
type Quality int

const (
	QualityBest Quality = iota
	QualityMiddle
	QualityLowest
)

// Preferences décrit ce que l'on veut d'un flux, indépendamment des
// identifiants propres à chaque playlist : elles s'appliquent à tout un lot.
type Preferences struct {
	Height       int      // hauteur visée en pixels ; 0 = meilleure qualité
	AudioLangs   []string // langues voulues (« fr », « en »…), par ordre de préférence
	AudioQuality Quality
	SubLangs     []string
}

// Selection est le résultat de l'application des préférences à un master.
type Selection struct {
	Variant Variant
	Audio   []Rendition
	Subs    []Rendition
	Notes   []string // choix de repli, à montrer à l'utilisateur
}

var langAliases = map[string]string{
	"fra": "fr", "fre": "fr", "eng": "en", "deu": "de", "ger": "de", "spa": "es", "ita": "it",
	"por": "pt", "nld": "nl", "dut": "nl", "ara": "ar", "rus": "ru", "jpn": "ja", "zho": "zh",
	"chi": "zh", "kor": "ko", "tur": "tr", "pol": "pl", "swe": "sv", "hin": "hi",
}

// LangKey normalise un code de langue : « fr-CA », « FRA », « fre » → « fr ».
func LangKey(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	if a, ok := langAliases[l]; ok {
		return a
	}
	return l
}

// renditionLang retourne la langue d'une piste, ou son nom à défaut.
func renditionLang(r Rendition) string {
	if k := LangKey(r.Language); k != "" {
		return k
	}
	name, _, _ := strings.Cut(r.Name, ",")
	return strings.ToLower(strings.TrimSpace(name))
}

// qualityRank devine la qualité d'une piste d'après son nom : 0 faible,
// 1 standard, 2 haute.
func qualityRank(r Rendition) int {
	n := strings.ToLower(r.Name)
	switch {
	case strings.Contains(n, "low") || strings.Contains(n, "basse") || strings.Contains(n, "faible"):
		return 0
	case strings.Contains(n, "high") || strings.Contains(n, "haute") || strings.Contains(n, "hd"):
		return 2
	}
	return 1
}

// LangInfo résume une langue disponible dans un master.
type LangInfo struct {
	Key      string // normalisée (« fr »)
	Label    string // « French (fr) »
	Versions int    // nombre de versions (low / high…) pour cette langue
	Default  bool   // au moins une piste par défaut
}

// Languages liste les langues d'un ensemble de pistes, dans l'ordre d'apparition.
func Languages(rs []Rendition) []LangInfo {
	idx := map[string]int{}
	var out []LangInfo
	for _, r := range rs {
		k := renditionLang(r)
		i, ok := idx[k]
		if !ok {
			name, _, _ := strings.Cut(r.Name, ",")
			name = strings.TrimSpace(name)
			label := name
			if name == "" {
				label = k
			} else if !strings.EqualFold(name, k) && k != "" {
				label = name + " (" + k + ")"
			}
			idx[k] = len(out)
			out = append(out, LangInfo{Key: k, Label: label})
			i = len(out) - 1
		}
		out[i].Versions++
		if r.Default {
			out[i].Default = true
		}
	}
	return out
}

// Select applique des préférences à un master.
func (m *Master) Select(p Preferences) (Selection, error) {
	if m == nil || len(m.Variants) == 0 {
		return Selection{}, errors.New(i18n.T("aucune variante vidéo"))
	}
	var sel Selection

	// Vidéo : meilleure, ou la plus proche sans dépasser la hauteur visée.
	vs := append([]Variant(nil), m.Variants...)
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].Bandwidth != vs[j].Bandwidth {
			return vs[i].Bandwidth > vs[j].Bandwidth
		}
		return vs[i].Height > vs[j].Height
	})
	sel.Variant = vs[0]
	if p.Height > 0 {
		best := -1
		for i, v := range vs {
			if v.Height > 0 && v.Height <= p.Height && (best < 0 || v.Height > vs[best].Height) {
				best = i
			}
		}
		if best < 0 { // tout dépasse : la plus petite
			for i, v := range vs {
				if best < 0 || (v.Height > 0 && v.Height < vs[best].Height) {
					best = i
				}
			}
		}
		if best >= 0 {
			sel.Variant = vs[best]
			if vs[best].Height > p.Height {
				sel.Notes = append(sel.Notes, "hauteur demandée indisponible : version la plus petite")
			}
		}
	}

	// Audio.
	all := m.AllRenditions("AUDIO")
	pickFor := func(pool []Rendition, langs []string, q Quality, kind string) []Rendition {
		var out []Rendition
		for _, want := range langs {
			key := LangKey(want)
			var cands []Rendition
			for _, r := range pool {
				if renditionLang(r) == key {
					cands = append(cands, r)
				}
			}
			if len(cands) == 0 {
				sel.Notes = append(sel.Notes, kind+" « "+want+" » absent")
				continue
			}
			sort.SliceStable(cands, func(i, j int) bool {
				if ri, rj := qualityRank(cands[i]), qualityRank(cands[j]); ri != rj {
					return ri < rj
				}
				return cands[i].GroupID < cands[j].GroupID
			})
			var chosen Rendition
			switch q {
			case QualityLowest:
				chosen = cands[0]
			case QualityMiddle:
				chosen = cands[len(cands)/2]
			default:
				chosen = cands[len(cands)-1]
			}
			out = append(out, chosen)
		}
		return out
	}
	sel.Audio = pickFor(all, p.AudioLangs, p.AudioQuality, "langue audio")
	if len(sel.Audio) == 0 && len(all) > 0 {
		// Rien de ce qui était demandé : on ne laisse pas la vidéo muette.
		var group []Rendition
		for _, r := range all {
			if sel.Variant.Audio != "" && r.GroupID == sel.Variant.Audio {
				group = append(group, r)
			}
		}
		pool := group
		if len(pool) == 0 {
			pool = all
		}
		fallback := pool[0]
		for _, r := range pool {
			if r.Default {
				fallback = r
				break
			}
		}
		sel.Audio = []Rendition{fallback}
		if len(p.AudioLangs) > 0 {
			sel.Notes = append(sel.Notes, "piste audio par défaut utilisée")
		}
	}

	// Sous-titres : première piste de chaque langue demandée.
	sel.Subs = pickFor(m.AllRenditions("SUBTITLES"), p.SubLangs, QualityBest, "sous-titres")
	return sel, nil
}
