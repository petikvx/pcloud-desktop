# pCloud Desktop

Client de bureau pour pCloud, en Go avec [Fyne](https://fyne.io).
Serveur utilisé : `eapi.pcloud.com` (datacenter européen) uniquement.

## Fonctionnalités

- **Connexion** par Google / pCloud (OAuth2), e-mail + mot de passe, ou clé API.
  Le jeton va dans le trousseau système ; le mot de passe n'est jamais stocké.
- **Navigateur** : liste en colonnes (nom, taille, date) triable, dossiers en
  premier, fil d'Ariane cliquable, précédent / dossier parent / actualiser,
  recherche dans le dossier, miniatures des images, icônes par type.
- **Téléchargement** d'un fichier, avec progression.
- **Envoi** de fichiers et de dossiers (bouton « Envoyer » ou glisser-déposer),
  avec progression et annulation.
- **Actions** (clic droit) : renommer, déplacer, supprimer, créer et copier un
  lien public ; création de dossier.
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

## Connexion Google / pCloud (OAuth2)

Un compte créé via Google n'a pas de mot de passe pCloud : il faut passer par
OAuth2.

1. Enregistrer une application sur pCloud (https://docs.pcloud.com → *My apps*)
   avec l'URL de redirection `http://localhost:8765/callback`.
2. Fournir ses identifiants par l'environnement (ne jamais les commiter) :

```sh
export PCLOUD_CLIENT_ID=...
export PCLOUD_CLIENT_SECRET=...
# optionnel, si l'URL de redirection utilise un autre port :
export PCLOUD_REDIRECT_PORT=8765
go run .
```

3. Onglet « Google / pCloud » → le navigateur s'ouvre sur la page de connexion
   pCloud, où l'on choisit Google.

## Structure

- `main.go` : point d'entrée, crée la fenêtre et applique le thème.
- `internal/pcloud/` : client de l'API pCloud, sans code d'interface.
  `client.go` (appels, erreurs), `api.go` (fichiers, transferts, miniatures),
  `oauth.go` (connexion OAuth2).
- `internal/ui/` : `ui.go` (connexion), `browser.go` (navigateur et actions),
  `row.go` (ligne de liste), `thumbs.go` (cache de miniatures), `format.go`
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
