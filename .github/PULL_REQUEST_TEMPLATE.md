<!-- Describe el cambio en una línea + contexto -->

## Cambios

<!-- Bullets concretos de qué cambia -->

-
-

## Por qué

<!-- Motivo. ¿Issue referenciado? -->

Closes #

## Checklist

- [ ] He añadido tests para el código nuevo (o actualizado existentes)
- [ ] `go test ./...` pasa en local
- [ ] `golangci-lint run` no añade warnings nuevos
- [ ] He actualizado documentación si cambia comportamiento público
- [ ] He añadido entrada en `CHANGELOG.md` bajo `## [Unreleased]`
- [ ] El commit usa [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, etc.)

## Tipo de cambio

- [ ] Bug fix (no rompe APIs existentes)
- [ ] Feature nueva (no rompe APIs existentes)
- [ ] Breaking change (rompe alguna API/env var/comportamiento)
- [ ] Solo documentación / build / CI
