#!/usr/bin/env bash
# Publie une nouvelle version : tests, .deb, tag, push et release GitHub.
#
# Usage : packaging/release.sh <version> [options]
#   <version>        numéro sans « v » (ex. 0.2.0)
#   -n, --dry-run    fait tout sauf modifier le dépôt ou GitHub
#   -y, --yes        ne demande pas de confirmation
#   --notes FICHIER  notes de version (sinon générées depuis l'historique git)
#   --draft          crée la release en brouillon
#
# Étapes : vérifications → tests → .deb → contrôle du paquet → notes →
#          confirmation → mise à jour des numéros dans README/script → commit +
#          push de main → tag → push du tag → gh release create.
#
# Prérequis : go, dpkg-deb, git, et gh authentifié (gh auth login).
set -euo pipefail

cd "$(dirname "$0")/.."
NAME="pcloud-desktop"
REPO="petikvx/pcloud-desktop"

die()  { echo "✘ $*" >&2; exit 1; }
info() { echo "→ $*"; }
ok()   { echo "✔ $*"; }

VERSION="" DRY=0 YES=0 DRAFT=0 NOTES_FILE=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--dry-run) DRY=1 ;;
        -y|--yes)     YES=1 ;;
        --draft)      DRAFT=1 ;;
        --notes)      shift; NOTES_FILE="${1:-}"; [[ -f "$NOTES_FILE" ]] || die "fichier de notes introuvable : $NOTES_FILE" ;;
        -h|--help)    sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        -*)           die "option inconnue : $1" ;;
        *)            [[ -z "$VERSION" ]] || die "une seule version attendue"; VERSION="$1" ;;
    esac
    shift
done
[[ -n "$VERSION" ]] || die "usage : $0 <version> [--dry-run] [--yes] [--notes FICHIER] [--draft]"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "version invalide : « $VERSION » (attendu X.Y.Z)"
TAG="v$VERSION"
ARCH="$(dpkg --print-architecture)"
DEB="dist/${NAME}_${VERSION}_${ARCH}.deb"
(( DRY )) && echo "(mode simulation : ni dépôt ni GitHub ne seront modifiés)"

# ---------------------------------------------------------------- vérifications
info "vérifications"
command -v gh >/dev/null        || die "gh n'est pas installé"
command -v dpkg-deb >/dev/null  || die "dpkg-deb est introuvable"
gh auth status >/dev/null 2>&1  || die "gh n'est pas authentifié (gh auth login)"

branch="$(git rev-parse --abbrev-ref HEAD)"
[[ "$branch" == "main" ]] || die "il faut être sur la branche main (actuellement : $branch)"

if [[ -n "$(git status --porcelain)" ]]; then
    if (( DRY )); then
        echo "  ⚠ arbre de travail non propre (toléré en simulation)"
    else
        die "arbre de travail non propre : commitez ou rangez vos modifications"
    fi
fi

git fetch --quiet origin main --tags
git merge-base --is-ancestor origin/main HEAD || die "main est en retard sur origin/main : faites un pull"

if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null || git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
    die "le tag $TAG existe déjà"
fi

prev="$(git tag --list 'v[0-9]*' --sort=-v:refname | head -n1 || true)"
if [[ -n "$prev" ]]; then
    newest="$(printf '%s\n%s\n' "${prev#v}" "$VERSION" | sort -V | tail -n1)"
    [[ "$newest" == "$VERSION" && "${prev#v}" != "$VERSION" ]] || die "$VERSION n'est pas supérieure à la dernière version ($prev)"
fi
ok "prêt à publier $TAG${prev:+ (précédente : $prev)}"

# ---------------------------------------------------------------- tests
info "tests"
go vet ./... >/dev/null 2>&1 || go vet -tags x11 ./... || die "go vet a échoué"
go test ./... >/dev/null 2>&1 || go test -tags x11 ./... || die "les tests ont échoué"
ok "tests réussis"

# ---------------------------------------------------------------- paquet
info "construction du .deb"
packaging/build-deb.sh "$VERSION" | sed 's/^/  /'
[[ -f "$DEB" ]] || die "paquet introuvable : $DEB"

info "contrôle du paquet"
got="$(dpkg-deb -f "$DEB" Version)"
[[ "$got" == "$VERSION" ]] || die "le paquet annonce la version $got au lieu de $VERSION"
chk="$(mktemp -d)"; trap 'rm -rf "$chk" "${NOTES_TMP:-}"' EXIT
dpkg-deb -x "$DEB" "$chk"
bin="$("$chk/usr/bin/$NAME" --version)"
[[ "$bin" == "$NAME $VERSION" ]] || die "le binaire annonce « $bin » au lieu de « $NAME $VERSION »"
ok "$DEB : version $got, binaire cohérent"

(cd dist && sha256sum "$(basename "$DEB")" > "$(basename "$DEB").sha256")
SUM="$(cut -d' ' -f1 "$DEB.sha256")"

# ---------------------------------------------------------------- notes
if [[ -n "$NOTES_FILE" ]]; then
    NOTES="$NOTES_FILE"
else
    NOTES_TMP="$(mktemp)"; NOTES="$NOTES_TMP"
    changes="$(git log ${prev:+"$prev..HEAD"} --no-merges --pretty='- %s' | grep -v -E '^- (Version |Merge )' || true)"
    {
        echo "## Installation (Debian / Ubuntu)"
        echo
        echo '```'
        echo "sudo apt install ./${NAME}_${VERSION}_${ARCH}.deb"
        echo '```'
        echo
        echo "## Changements"
        echo
        if [[ -n "$changes" ]]; then
            echo "$changes"
        elif [[ -n "$prev" ]]; then
            echo "- Aucun changement depuis $prev (paquet reconstruit)."
        else
            echo "- Première version."
        fi
        echo
        echo "## Vérification"
        echo
        echo "SHA-256 : \`$SUM\`"
    } > "$NOTES"
fi

echo
echo "──────────── notes de la release $TAG ────────────"
cat "$NOTES"
echo "──────────────────────────────────────────────────"

# ---------------------------------------------------------------- confirmation
if (( DRY )); then
    echo
    ok "simulation terminée : rien n'a été publié."
    echo "  Paquet : $DEB"
    echo "  Pour publier : $0 $VERSION"
    exit 0
fi

echo
echo "Va être fait sur $REPO :"
echo "  1. mise à jour des numéros de version dans README.md et packaging/build-deb.sh (commit si besoin)"
echo "  2. push de main"
echo "  3. création et push du tag $TAG"
draft_note=""; (( DRAFT )) && draft_note=" (brouillon)"
echo "  4. release GitHub publique${draft_note} avec $(basename "$DEB") et son empreinte"
if (( ! YES )); then
    read -r -p "Continuer ? [o/N] " answer </dev/tty
    [[ "$answer" =~ ^[oOyY]$ ]] || die "abandonné"
fi

# ---------------------------------------------------------------- publication
if [[ -n "$prev" ]]; then
    sed -i -E "s/${NAME}_[0-9]+\.[0-9]+\.[0-9]+_/${NAME}_${VERSION}_/g; s#(build-deb\.sh) [0-9]+\.[0-9]+\.[0-9]+#\1 ${VERSION}#g" README.md
    sed -i -E "s/VERSION=\"\\$\{1:-[0-9]+\.[0-9]+\.[0-9]+\}\"/VERSION=\"\${1:-${VERSION}}\"/; s/\(défaut : [0-9]+\.[0-9]+\.[0-9]+\)/(défaut : ${VERSION})/" packaging/build-deb.sh
fi
if ! git diff --quiet; then
    info "commit des numéros de version"
    git add README.md packaging/build-deb.sh
    git commit -q -m "Version $VERSION"
fi

info "push de main"
git push origin main

info "tag $TAG"
git tag -a "$TAG" -m "$NAME $VERSION"
git push origin "$TAG"

info "release GitHub"
args=("$TAG" "$DEB" "$DEB.sha256" --repo "$REPO" --title "pCloud Desktop $VERSION" --notes-file "$NOTES" --verify-tag)
(( DRAFT )) && args+=(--draft)
if ! gh release create "${args[@]}"; then
    echo "✘ la release n'a pas pu être créée, mais le tag $TAG est poussé." >&2
    echo "  Réessayez : gh release create $TAG $DEB $DEB.sha256 --repo $REPO --title \"pCloud Desktop $VERSION\" --notes-file <notes>" >&2
    exit 1
fi
ok "release publiée : https://github.com/$REPO/releases/tag/$TAG"
