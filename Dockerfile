# Dockerfile simple pour le projet QuelPoke
# Utilise une image golang officielle, copie les fichiers, compile et lance le binaire
FROM golang:1.21

WORKDIR /app

# Copier les fichiers du projet
COPY . .

# Exposer le port utilisé par l'application
EXPOSE 8080

# Lancer le binaire (le binaire s'appuie sur main.go)
CMD ["go", "run", "main.go"]
