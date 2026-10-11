#!/usr/bin/env bash
# Construit le paquet .deb de pcloud-desktop (Wayland + X11).
#
# Usage : packaging/build-deb.sh [version]      (défaut : 0.1.0)
# Sortie : dist/pcloud-desktop_<version>_<arch>.deb
#
# Prérequis : go, gcc, dpkg-dev, et les en-têtes de développement de Fyne
#   sudo apt install gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev libwayland-dev
set -euo pipefail

VERSION="${1:-0.1.0}"
NAME="pcloud-desktop"
MAINTAINER="PetiK <petikvx@gmail.com>"
HOMEPAGE="https://github.com/petikvx/pcloud-desktop"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARCH="$(dpkg --print-architecture)"
OUT="$ROOT/dist"
PKG="$OUT/${NAME}_${VERSION}_${ARCH}"

[[ "$VERSION" =~ ^[0-9][0-9A-Za-z.+~-]*$ ]] || { echo "version invalide : $VERSION" >&2; exit 1; }

rm -rf "$PKG"
mkdir -p "$PKG/DEBIAN" "$PKG/usr/bin" \
         "$PKG/usr/share/applications" \
         "$PKG/usr/share/icons/hicolor/256x256/apps" \
         "$PKG/usr/share/doc/$NAME"

echo "→ compilation (Wayland + X11)"
(cd "$ROOT" && CGO_ENABLED=1 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "$PKG/usr/bin/$NAME" .)
chmod 0755 "$PKG/usr/bin/$NAME"

# Dépendances : paquets Debian qui fournissent les bibliothèques réellement liées.
echo "→ calcul des dépendances"
deps="$(ldd "$PKG/usr/bin/$NAME" \
    | awk '/=> \//{print $3}' \
    | xargs -r readlink -f \
    | xargs -r dpkg-query -S 2>/dev/null \
    | sed 's/:.*//; s/:[a-z0-9]*$//' \
    | sort -u | paste -sd, - | sed 's/,/, /g')"
[[ -n "$deps" ]] || { echo "aucune dépendance détectée : ldd a échoué ?" >&2; exit 1; }

# Bibliothèques chargées à l'exécution par GLFW (dlopen) : invisibles pour ldd.
# Vérifiables avec : strings -a pcloud-desktop | grep -E '^lib.*\.so'
dlopen_deps="libxkbcommon0, libwayland-cursor0, libwayland-egl1, libxcursor1, libxinerama1, libxi6, libxrandr2, libxxf86vm1"
deps="$deps, $dlopen_deps"

install -m 0644 "$ROOT/packaging/icon.png" \
    "$PKG/usr/share/icons/hicolor/256x256/apps/$NAME.png"

cat > "$PKG/usr/share/applications/$NAME.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=pCloud Desktop
Comment=Client de bureau pour pCloud
Comment[en]=Desktop client for pCloud
Exec=$NAME
Icon=$NAME
Terminal=false
Categories=Network;FileTransfer;
Keywords=pcloud;cloud;stockage;hls;
StartupWMClass=fr.krmr.pcloud-desktop
DESKTOP
chmod 0644 "$PKG/usr/share/applications/$NAME.desktop"

# Fichier copyright au format machine-lisible de Debian.
cat > "$PKG/usr/share/doc/$NAME/copyright" <<COPYRIGHT
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: $NAME
Source: $HOMEPAGE

Files: *
Copyright: 2026 PetiK <petikvx@gmail.com>
License: MIT

License: MIT
$(sed -e 's/^$/./' -e 's/^/ /' "$ROOT/LICENSE" | tail -n +3)
COPYRIGHT
chmod 0644 "$PKG/usr/share/doc/$NAME/copyright"

SIZE_KB="$(du -sk "$PKG/usr" | cut -f1)"
cat > "$PKG/DEBIAN/control" <<CONTROL
Package: $NAME
Version: $VERSION
Section: utils
Priority: optional
Architecture: $ARCH
Maintainer: $MAINTAINER
Installed-Size: $SIZE_KB
Depends: $deps
Recommends: ffmpeg, libdecor-0-0, libegl1
Homepage: $HOMEPAGE
Description: Client de bureau pour pCloud
 Navigateur de fichiers pCloud avec transferts, téléchargement de flux
 vidéo HLS (qualité, langues audio, sous-titres) et de dossiers entiers.
 .
 ffmpeg est facultatif : il sert à assembler vidéo, audio et sous-titres
 en un seul fichier MP4.
CONTROL

# Debian attend des dossiers en 0755 quel que soit l'umask.
find "$PKG" -type d -exec chmod 0755 {} +

echo "→ construction du paquet"
dpkg-deb --root-owner-group --build "$PKG" "$OUT/${NAME}_${VERSION}_${ARCH}.deb" >/dev/null
rm -rf "$PKG"

DEB="$OUT/${NAME}_${VERSION}_${ARCH}.deb"
echo "✔ $DEB ($(du -h "$DEB" | cut -f1))"
echo "  Dépendances : $deps"
echo "  Recommandés : ffmpeg, libdecor-0-0, libegl1"
echo "  Installer   : sudo apt install $DEB"
