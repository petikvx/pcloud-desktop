package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// literal retourne la valeur d'une expression chaîne constante (littéraux et
// concaténations de littéraux).
func literal(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, ok1 := literal(v.X)
		r, ok2 := literal(v.Y)
		return l + r, ok1 && ok2
	case *ast.ParenExpr:
		return literal(v.X)
	}
	return "", false
}

// sourceKeys liste les textes passés à tr, trf, i18n.T, i18n.Tf et i18n.N dans
// le code de l'application (hors tests). Les appels dont l'argument n'est pas
// un littéral sont signalés dans dynamic.
func sourceKeys(t *testing.T) (keys map[string][]string, dynamic []string) {
	t.Helper()
	keys = map[string][]string{}
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "dist" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			var name string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				if id, ok := fn.X.(*ast.Ident); ok && id.Name == "i18n" {
					name = "i18n." + fn.Sel.Name
				}
			}
			nargs := 0
			switch name {
			case "tr", "trf", "i18n.T", "i18n.Tf":
				nargs = 1
			case "i18n.N":
				nargs = 3 // n, singulier, pluriel
			default:
				return true
			}
			where := fset.Position(call.Pos()).String()
			for _, a := range call.Args[nargs-1 : nargs] {
				_ = a
			}
			idx := []int{0}
			if name == "i18n.N" {
				idx = []int{1, 2}
			}
			for _, i := range idx {
				if i >= len(call.Args) {
					continue
				}
				if s, ok := literal(call.Args[i]); ok {
					keys[s] = append(keys[s], where)
				} else {
					dynamic = append(dynamic, where)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys, dynamic
}

// Toute chaîne de l'interface a une traduction anglaise, et inversement.
func TestEnglishCatalogIsComplete(t *testing.T) {
	keys, _ := sourceKeys(t)

	var missing, stale []string
	for k := range keys {
		if _, ok := en[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range en {
		if _, ok := keys[k]; !ok && !extraKeys[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("%d texte(s) sans traduction anglaise :\n%q", len(missing), strings.Join(missing, "\"\n\""))
	}
	if len(stale) > 0 {
		t.Errorf("%d traduction(s) inutilisée(s) :\n%q", len(stale), strings.Join(stale, "\"\n\""))
	}
}

// Aucun appel de traduction ne reçoit une chaîne calculée : elle échapperait au
// catalogue.
func TestNoDynamicKeys(t *testing.T) {
	if _, dynamic := sourceKeys(t); len(dynamic) > 0 {
		t.Errorf("appels de traduction avec une clé non littérale :\n%s", strings.Join(dynamic, "\n"))
	}
}

var verbRe = regexp.MustCompile(`%[-+# 0]*(?:\d+|\*)?(?:\.\d+)?[a-zA-Z%]`)

// La traduction garde les mêmes verbes fmt que la source, dans le même ordre :
// sinon Sprintf afficherait n'importe quoi.
func TestEnglishKeepsFormatVerbs(t *testing.T) {
	for k, v := range en {
		a, b := verbRe.FindAllString(k, -1), verbRe.FindAllString(v, -1)
		if strings.Join(a, " ") != strings.Join(b, " ") {
			t.Errorf("verbes différents :\n  fr %q %v\n  en %q %v", k, a, v, b)
		}
	}
}

func TestEnglishDiffersFromFrench(t *testing.T) {
	for k, v := range en {
		if k == v && !sameInBoth[k] {
			t.Errorf("traduction identique au français (à supprimer ou à déclarer) : %q", k)
		}
	}
}

func TestSwitchLanguage(t *testing.T) {
	defer Set(FR)
	Set("en-US")
	if got := T("Annuler"); got != "Cancel" {
		t.Fatalf("T = %q", got)
	}
	Set("fr_FR.UTF-8")
	if got := T("Annuler"); got != "Annuler" {
		t.Fatalf("T = %q", got)
	}
	Set("xx")
	if Current() != FR {
		t.Fatalf("langue inconnue : %q", Current())
	}
	if got := T("texte absent du catalogue"); got != "texte absent du catalogue" {
		t.Fatalf("repli = %q", got)
	}
}

func TestPlural(t *testing.T) {
	defer Set(FR)
	cases := []struct {
		lang string
		n    int
		want string
	}{
		{FR, 0, "0 élément"}, {FR, 1, "1 élément"}, {FR, 2, "2 éléments"},
		{EN, 0, "0 items"}, {EN, 1, "1 item"}, {EN, 2, "2 items"},
	}
	for _, c := range cases {
		Set(c.lang)
		if got := N(c.n, "%d élément", "%d éléments"); got != c.want {
			t.Errorf("%s n=%d : %q, attendu %q", c.lang, c.n, got, c.want)
		}
	}
}

func TestDetect(t *testing.T) {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		t.Setenv(k, "")
	}
	if got := Detect(); got != EN {
		t.Fatalf("sans variable : %q", got)
	}
	t.Setenv("LANG", "fr_FR.UTF-8")
	if got := Detect(); got != FR {
		t.Fatalf("LANG=fr_FR : %q", got)
	}
	t.Setenv("LANGUAGE", "de:fr")
	if got := Detect(); got != FR {
		t.Fatalf("LANGUAGE=de:fr : %q", got)
	}
	t.Setenv("LC_ALL", "en_GB.UTF-8")
	if got := Detect(); got != EN {
		t.Fatalf("LC_ALL prioritaire : %q", got)
	}
}
