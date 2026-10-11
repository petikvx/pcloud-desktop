# pCloud Desktop

Client de bureau pour pCloud, en Go avec [Fyne](https://fyne.io).
Serveur utilisé : `eapi.pcloud.com` (datacenter européen) uniquement.

## Fonctionnalités

### Navigation et gestion

- **Connexion** par e-mail + mot de passe, ou par clé API (jeton `auth` pCloud).
  Le jeton va dans le trousseau système ; le mot de passe n'est jamais stocké.
  Retour automatique à l'écran de connexion si la session expire.
- **Navigateur** : liste en colonnes (nom, taille, date) triable, dossiers en
  premier, fil d'Ariane cliquable, précédent / dossier parent / actualiser,
  recherche dans le dossier, miniatures des images, icônes par type.
- **Actions** (clic droit) : renommer, déplacer, supprimer, créer et copier un
  lien public ; création de dossier.
- **Envoi** de fichiers et de dossiers (bouton « Envoyer » ou glisser-déposer).
- **Téléchargement** d'un fichier.
- **Reprise des transferts** : un téléchargement ou un envoi interrompu repart
  de l'octet où il s'est arrêté (voir plus bas).
- Progression de tous les transferts avec **débit** et **temps restant**, et
  annulation. Quota utilisé affiché dans la barre d'état.

### Reprise des transferts

- **Téléchargements** : le contenu arrive dans `fichier.part` (avec un petit
  `fichier.part.json` qui identifie la version distante) et n'est renommé qu'à
  la fin. Après une coupure réseau, l'application réessaie seule (attentes de
  1 s, 2 s, 4 s… jusqu'à 30 s, 5 échecs consécutifs) et reprend avec un en-tête
  `Range`. Après une annulation, une erreur ou une fermeture de l'application,
  relancer le téléchargement vers le **même fichier** reprend aussi à l'octet
  près ; le fichier partiel est jeté si le fichier distant a changé
  (taille, date, `ETag`) ou si le serveur ignore `Range`.
  Le téléchargement d'un dossier en profite : relancer le lot reprend le
  fichier interrompu.
- **Envois** : les fichiers de plus de 8 Mio partent par morceaux de 8 Mio
  (`upload_create`, `upload_write`, `upload_save`). Après une coupure, le client
  demande au serveur combien d'octets il a reçus (`upload_info`) et continue
  à cet endroit. L'identifiant d'envoi est mémorisé dans
  `~/.cache/pcloud-desktop/uploads.json` (3 jours) : relancer l'envoi du même
  fichier vers le même dossier, même après un redémarrage, reprend l'envoi.
  Les petits fichiers partent en une requête, réessayée en cas de coupure.
- Les erreurs définitives (refus 4xx, session expirée, quota dépassé) ne sont
  pas réessayées.

### Langues

L'interface existe en **français** et en **anglais**. La langue est choisie
d'après l'environnement (`LC_ALL`, `LC_MESSAGES`, `LANGUAGE`, `LANG` ; anglais
si rien de reconnu), puis modifiable par le bouton **FR / EN** de la barre
d'outils ou de l'écran de connexion ; le choix est mémorisé.

Pour ajouter ou corriger une traduction : les textes sources sont en français
dans le code (`tr("…")`, `trf("…", args)`, `i18n.T`, `i18n.Tf`, `i18n.N`) et
la table anglaise est dans `internal/i18n/en.go`, indexée par ce texte. Un
texte sans traduction s'affiche en français. `go test ./internal/i18n` vérifie
que tous les textes de l'application sont traduits, qu'aucune traduction n'est
orpheline et que les verbes de format (`%s`, `%d`…) correspondent.

### Vidéos en streaming (HLS)

- **Version streaming d'une vidéo** (clic droit sur une vidéo → « Télécharger une
  version streaming… ») : l'application demande à pCloud le lien de streaming
  (`getmediatranscodelink`, la méthode du site web) et ouvre l'outil HLS dessus.
  Si pCloud ne fournit pas de playlist, elle se rabat sur les variantes de
  l'API publique (`getvideolinks`), ou sur un transcodage personnalisé.
- **Outil « Flux HLS »** (bouton de la barre d'outils) : lit un `master.m3u8`
  (URL ou fichier local) et télécharge le flux, en Go pur, sans yt-dlp.
  - choix de la **qualité vidéo**, des **pistes audio par langue et par version**
    (low / standard / high, indépendamment de la qualité vidéo) et des
    **sous-titres** ;
  - segments téléchargés **en parallèle** (réglable, 6 par défaut, comme
    `yt-dlp -N`), avec reprises sur erreur ;
  - chiffrement AES-128 géré ;
  - avec **ffmpeg** installé : un seul **MP4**, sans ré-encodage, avec les
    pistes audio (langue et nom conservés) et les sous-titres choisis ; sans
    ffmpeg : vidéo, audio et sous-titres en fichiers séparés (`.ts` / `.vtt`) ;
  - non géré : SAMPLE-AES / DRM, flux en direct.
- **Téléchargement d'un dossier entier** (clic droit sur un dossier →
  « Télécharger le dossier (streaming)… ») :
  1. le dossier est exploré avec ses sous-dossiers ;
  2. on choisit **une seule fois** la qualité vidéo, les langues audio, la
     version de l'audio et les sous-titres (mémorisés pour la fois suivante) ;
  3. l'arborescence est recréée en local, puis chaque vidéo est téléchargée en
     **file d'attente** avec les mêmes préférences ;
  4. un échec n'arrête pas le lot, et on peut relancer : les fichiers déjà
     présents sont ignorés. Les fichiers non vidéo peuvent être inclus.

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

**ffmpeg** est facultatif : il sert uniquement à assembler vidéo, audio et
sous-titres en un MP4 (`sudo apt install ffmpeg`).

## Installer (paquet .deb)

```sh
sudo apt install ./pcloud-desktop_0.1.1_amd64.deb
pcloud-desktop --version
```

Le paquet (Wayland + X11) installe le binaire, l'entrée de menu et l'icône.
`ffmpeg` est recommandé, pas obligatoire.

### Construire le .deb

```sh
sudo apt install gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev libwayland-dev
packaging/build-deb.sh 0.1.1      # produit dist/pcloud-desktop_0.1.1_amd64.deb
```

Les dépendances sont déduites des bibliothèques réellement liées au binaire
(`ldd`), complétées de celles que GLFW charge à l'exécution.

### Publier une version

```sh
packaging/release.sh 0.2.0 --dry-run   # tout vérifier et construire, sans rien publier
packaging/release.sh 0.2.0             # publier (demande confirmation)
```

Le script vérifie que l'arbre est propre et à jour, lance les tests, construit
et contrôle le `.deb`, génère les notes depuis l'historique git, puis commite
les numéros de version, pousse `main`, pose le tag `vX.Y.Z` et crée la release
GitHub (paquet + empreinte SHA-256). Il nécessite `gh` authentifié.
Options : `--yes` (sans confirmation), `--notes FICHIER`, `--draft`.

## Compte créé avec Google

Un compte créé via « Se connecter avec Google » n'a pas de mot de passe pCloud.
Deux possibilités : définir un mot de passe (« mot de passe oublié » sur le site
pCloud), puis se connecter par e-mail ; ou coller le jeton `auth` dans l'onglet
« Clé API » (il est ensuite mémorisé dans le trousseau).

## Structure

- `main.go` : point d'entrée, crée la fenêtre et applique le thème.
- `packaging/` : `build-deb.sh` (paquet), `release.sh` (publication), icône.
- `internal/pcloud/` : client de l'API pCloud, sans code d'interface.
  - `client.go` : appels, erreurs, session ;
  - `api.go` : fichiers, dossiers, miniatures, envoi en une requête ;
  - `download.go` : téléchargement reprenable (`.part`, `Range`, `If-Range`) ;
  - `upload.go` : envoi par morceaux reprenable (`upload_*`) ;
  - `retry.go` : erreurs transitoires et attentes entre les tentatives ;
  - `video.go` : streaming (`getmediatranscodelink`, `getvideolinks`).
- `internal/hls/` : playlists HLS, sans dépendance externe.
  - `parse.go` : playlists maître et média ;
  - `download.go` : téléchargement parallèle, AES-128, byte ranges ;
  - `select.go` : application de préférences (hauteur, langues, version audio).
- `internal/queue/` : file d'attente séquentielle avec suivi d'état.
- `internal/i18n/` : traductions (français, anglais) et détection de la langue.
- `internal/uploadstate/` : mémoire sur disque des envois en cours.
- `internal/ui/` : interface Fyne.
  - `ui.go` : connexion ; `browser.go` : navigateur et actions ;
  - `row.go`, `thumbs.go`, `theme.go`, `format.go` : liste, miniatures, thème,
    tri / filtre / formats ;
  - `video.go`, `hlstool.go`, `ffmpeg.go` : streaming, outil HLS, assemblage MP4 ;
  - `folderdl.go`, `folderjob.go` : téléchargement de dossiers ;
  - `meter.go` : débit et temps restant.

## Notes

- Les appels réseau tournent dans des goroutines et reviennent dans le thread
  d'interface par `fyne.Do`.
- Les paramètres d'API partent en POST (corps du formulaire) : mot de passe et
  jeton ne figurent jamais dans une URL.
- Les fichiers sont écrits en `.part` puis renommés à la fin : pas de fichier
  partiel pris pour un fichier complet.
- Exception à la règle des paramètres en POST : `upload_write` reçoit le contenu
  brut du morceau comme corps de requête, ses paramètres (dont le jeton) sont
  donc dans l'URL, en HTTPS.
- `getmediatranscodelink` n'est pas documenté officiellement : sa réponse est
  interprétée de façon défensive, et son JSON brut est affiché en cas de forme
  inattendue.

## Tests

```sh
go test ./...
go test -race ./...    # avec détection de courses
```

Les tests utilisent des serveurs HTTP factices (aucun accès à un vrai compte).
Un test d'intégration utilise le vrai `ffmpeg` s'il est installé, et se saute
sinon.

## Licence

MIT — voir [LICENSE](LICENSE).
