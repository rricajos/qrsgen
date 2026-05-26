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

## Glosario

**GHCR**: GitHub Container Registry. Registry de imágenes Docker
integrado con GitHub Actions. `ghcr.io/rricajos/qrsgen`.

**Multi-arch image**: una sola tag que internamente apunta a imágenes
distintas para amd64 / arm64. Docker descarga la adecuada para tu host.

**Cosign**: herramienta para firmar y verificar artefactos Docker.
qrsgen firma sus imágenes y SBOMs en cada release.

**Distroless**: imagen Docker minimalista sin shell ni package
manager. Solo el binario + libs estrictamente necesarias.

**Multi-stage build**: Dockerfile con varias etapas (`FROM ... AS ...`).
Permite compilar en una imagen grande (`golang:1.26-alpine`) y copiar
solo el binario a una pequeña (`distroless`).

**SBOM** (Software Bill of Materials): inventario formal de las
dependencias del binario. qrsgen lo genera por arquitectura para
audit trail / vulnerability scanning.

**Checksum firmado**: hash del artefacto acompañado de una firma
cosign. Verifica integridad + autenticidad.

**GoReleaser**: herramienta que automatiza builds multi-arch, dockers,
SBOMs y firmas. qrsgen lo dispara en cada tag `v*`.
