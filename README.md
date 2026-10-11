# pCloud Desktop

Client de bureau pour pCloud, en Go avec [Fyne](https://fyne.io).
Serveur utilisé : `eapi.pcloud.com` (datacenter européen) uniquement.

## Fonctionnalités

- **Connexion** par e-mail + mot de passe, ou par clé API (jeton `auth` pCloud).
  Le jeton va dans le trousseau système ; le mot de passe n'est jamais stocké.
- **Navigateur** : liste en colonnes (nom, taille, date) triable, dossiers en
  premier, fil d'Ariane cliquable, précédent / dossier parent / actualiser,
  recherche dans le dossier, miniatures des images, icônes par type.
- **Téléchargement** d'un fichier, avec progression.
- **Version streaming des vidéos** (clic droit → « Télécharger une version
  streaming… ») : choix parmi les variantes déjà disponibles (résolution,
  codecs, débits) ou transcodage personnalisé par pCloud (résolution, débit
  vidéo et audio). La langue n'est pas gérée : l'API pCloud n'expose pas les
  pistes audio ; le fichier original, lui, les conserve toutes.
- **Outil « Flux HLS »** (bouton de la barre d'outils) : lit un `master.m3u8`
  (URL ou fichier local) et télécharge le flux, en Go pur, sans yt-dlp.
  Choix de la qualité, des **pistes audio par langue et par version**
  (low / normal / high, indépendamment de la qualité vidéo) et des sous-titres ;
  segments téléchargés en parallèle (réglable, 6 par défaut, comme `yt-dlp -N`),
  chiffrement AES-128 géré. Si ffmpeg est
  installé, le résultat est assemblé en un seul **MP4** (sans ré-encodage) avec
  les pistes audio et sous-titres choisis, langues comprises ; sinon, vidéo,
  audio et sous-titres sont enregistrés en fichiers séparés (.ts / .vtt).
  Non géré : SAMPLE-AES / DRM, flux en direct.
- **Téléchargement d'un dossier entier** (clic droit sur un dossier →
  « Télécharger le dossier (streaming)… ») : on choisit **une fois** la qualité
  vidéo et les langues (audio, version low / high, sous-titres), le dossier est
  recréé en local avec ses sous-dossiers, puis chaque vidéo est téléchargée en
  file d'attente avec les mêmes préférences. Un échec n'arrête pas le lot ; on
  peut relancer : les fichiers déjà présents sont ignorés.
- **Envoi** de fichiers et de dossiers (bouton « Envoyer » ou glisser-déposer),
  avec progression et annulation.
- **Actions** (clic droit) : renommer, déplacer, supprimer, créer et copier un
  lien public ; création de dossier.
- Débit et temps restant affichés pendant les transferts.
- Quota utilisé affiché dans la barre d'état ; retour automatique à l'écran de
  connexion si la session expire.

### Raccourcis

| Touche | Action |
|---|---|
| Double-clic / `Entrée` | Ouvrir un dossier, télécharger un fichier |
| `Retour arrière` | Dossier parent |
| `F5` | Actualiser |
| `F2` | Renommer |
| `Suppr` | Supprimer |
| `Ctrl+F` | Rechercher dans le dossier |

## Lancer

```sh
go mod tidy      # télécharge Fyne et go-keyring
go run .
```

Fyne utilise cgo : il faut un compilateur C et quelques paquets système
(Linux : `gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev libwayland-dev`).
Sans les en-têtes Wayland, compiler en X11 seulement : `go run -tags x11 .`.

## Compte créé avec Google

Un compte créé via « Se connecter avec Google » n'a pas de mot de passe pCloud.
Deux possibilités : définir un mot de passe (« mot de passe oublié » sur le site
pCloud), puis se connecter par e-mail ; ou coller le jeton `auth` dans l'onglet
« Clé API » (il est ensuite mémorisé dans le trousseau).

## Structure

- `main.go` : point d'entrée, crée la fenêtre et applique le thème.
- `internal/pcloud/` : client de l'API pCloud, sans code d'interface.
  `client.go` (appels, erreurs), `api.go` (fichiers, transferts, miniatures), `video.go` (streaming),
- `internal/hls/` : lecture des playlists HLS et téléchargement des segments.
- `internal/queue/` : file d'attente séquentielle avec suivi d'état.
- `internal/ui/` : `ui.go` (connexion), `browser.go` (navigateur et actions),
  `row.go` (ligne de liste), `video.go` (choix du format), `hlstool.go` (outil HLS), `folderdl.go` / `folderjob.go` (dossiers), `thumbs.go` (cache de miniatures), `format.go`
  (tri, filtre, formats), `theme.go`.

## Notes

- Les appels réseau tournent dans des goroutines et reviennent dans le thread
  d'interface par `fyne.Do`.
- Les paramètres d'API partent en POST (corps du formulaire) : mot de passe et
  jeton ne figurent jamais dans une URL.
- Le téléchargement de dossiers entiers n'est pas géré (fichiers seulement).

## Tests

```sh
go test ./...
```
