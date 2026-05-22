# Security policy

## Supported versions

Solo la última versión `main` recibe parches de seguridad. Las releases etiquetadas (v0.x.y) mantienen su estado en el momento del tag — sin backports.

## Reporting a vulnerability

Si encuentras una vulnerabilidad:

1. **NO abras un issue público.**
2. Envía un email a **rricajos@protonmail.com** con asunto `[qrsgen] vulnerability`.
3. Incluye:
   - Descripción técnica
   - Steps to reproduce (PoC si lo tienes)
   - Versión afectada
   - Impacto estimado

Idealmente cifra con GPG (key disponible en https://keys.openpgp.org buscando rricajos@protonmail.com).

## Response

- **Confirmación**: 48h.
- **Triage inicial**: 5 días laborables.
- **Patch**: dependiendo de severidad, entre 7 y 30 días.
- **Disclosure**: coordinada con el reporter. Por defecto, 90 días tras patch.

## Out of scope

- Riesgo de baneo de número WhatsApp por uso de la API no oficial (ver [DISCLAIMER.md](DISCLAIMER.md)) — **conocido y documentado, no es una vulnerabilidad**.
- Issues que requieren acceso root al host del usuario para explotarse.
- Self-XSS o ataques que requieren engaño activo al admin.

## Reconocimiento

Damos crédito a reporters en el CHANGELOG.md si lo desean (incluye tu nombre/handle en el report).
