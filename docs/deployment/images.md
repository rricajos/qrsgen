# Imágenes

## 1. Imagen pre-built desde GHCR (recomendado)

```bash
docker pull ghcr.io/rricajos/qrsgen:0.23.0-rc1
# o el tag :latest
```

Multi-arch (amd64 + arm64). Firmada con cosign. Construida por GoReleaser
en cada tag `vX.Y.Z` desde GitHub Actions.

## 2. Build local desde el repo

```bash
cd /opt/qrsgen
docker build -t qrsgen:0.23.0-rc1 .
```

Multi-stage build:
`golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot`.
Imagen final ~25 MB.

## 3. Binario nativo desde release

Para deploys sin Docker (raros pero posibles):

```bash
curl -L -o qrsgen.tar.gz \
  https://github.com/rricajos/qrsgen/releases/download/v0.23.0-rc1/qrsgen_0.23.0-rc1_linux_amd64.tar.gz
tar xzf qrsgen.tar.gz
chmod +x qrsgen
./qrsgen   # lee env vars; ver tabla en "Variables de entorno"
```

Cada binario lleva SBOM (`*.sbom.json`) + checksum firmado.
