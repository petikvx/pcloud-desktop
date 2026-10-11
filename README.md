# pCloud Desktop

Client de bureau pour pCloud, en Go avec [Fyne](https://fyne.io).

## Lancer

```sh
go mod tidy      # télécharge Fyne et go-keyring, génère go.sum
go run .
```

Fyne utilise cgo : il faut un compilateur C et, selon l'OS, quelques paquets
système. Voir https://docs.fyne.io/started/ (Linux : `gcc libgl1-mesa-dev xorg-dev`).

## Structure

- `main.go` : point d'entrée, crée la fenêtre.
- `internal/pcloud/` : client de l'API pCloud (connexion, listing). Aucun code d'interface.
- `internal/ui/` : écran de connexion et navigateur de dossiers.

## Notes

- Connexion : essaie `eapi.pcloud.com` (Europe) puis `api.pcloud.com` (US).
- Le mot de passe n'est jamais stocké ; seul le jeton de session va dans le
  trousseau système (`go-keyring`).
- Appels réseau dans des goroutines pour ne pas figer l'interface.

## Prochaines étapes

1. Téléchargement (`getfilelink` puis GET sur l'URL renvoyée)
2. Envoi (`uploadfile`) avec barre de progression
3. Créer / renommer / supprimer (`createfolder`, `renamefile`, `deletefile`)
4. Miniatures (`getthumblink`)
