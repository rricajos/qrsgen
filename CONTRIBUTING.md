# Contributing to qrsgen

Gracias por tu interés. Las contribuciones son bienvenidas.

## Dev setup

```bash
git clone https://github.com/rricajos/qrsgen.git
cd qrsgen
go mod download
go test ./...
```

Para correr el binario en local necesitas:

- Postgres 14+ accesible
- Variables de entorno (ver `.env.example`)

```bash
export $(cat .env.example | grep -v '^#' | xargs)
go run ./cmd/server
```

## Coding style

- **gofmt** (automático en `go build`).
- **golangci-lint** debe pasar — `.golangci.yml` define el set.
- **Comentarios** en español si tocan código existente que ya lo está, inglés si añades algo nuevo. Sé pragmático.
- **Errores envueltos** con `fmt.Errorf("contexto: %w", err)`.
- **Loggers estructurados** con `slog`, no `fmt.Println`.

## Pull requests

1. **Fork** + branch desde `main` (ej. `feat/multi-tenant`, `fix/qr-rotation-race`).
2. Commits con [Conventional Commits](https://www.conventionalcommits.org/):
   - `feat: add foo`
   - `fix: handle empty content in spamguard`
   - `chore(deps): bump whatsmeow`
   - `docs: clarify webhook payload`
3. **Tests** para código nuevo. Bug fixes requieren un test que falle ANTES y pase DESPUÉS.
4. **Actualiza** docs si cambias comportamiento público.
5. **CHANGELOG.md** entry bajo `## [Unreleased]`.
6. PR description con: contexto, qué cambia, cómo testarlo.

## Cambios breaking

Si tu cambio rompe la API HTTP o env vars existentes, marca el PR con label `breaking-change` y describe la migración en el CHANGELOG.

## Testing

```bash
# Unit
go test ./...

# Race detector
go test -race ./...

# Coverage
go test -coverprofile=cover.out ./... && go tool cover -html=cover.out
```

Tests con DB requieren `POSTGRES_*` env vars apuntando a una DB de testing (puedes usar `docker run -d -e POSTGRES_PASSWORD=test postgres:16`).

## Áreas donde ayuda está especialmente bienvenida

- Multi-tenant real (un proceso qrsgen sirviendo múltiples downstream systems con distintos tokens).
- Tests más extensivos (coverage actual ~10% en bridge).
- Adaptadores para otros sistemas de chat (más allá del formato `Channel::Api`).
- Métricas adicionales (latencia send_text, distribución por tipo de adjunto, etc.).
- Documentación / traducciones.

## Code of conduct

Ver [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Licencia

Al contribuir aceptas que tu código se distribuye bajo la licencia MIT del proyecto.
