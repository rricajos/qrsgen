# Licencia y avisos legales

qrsgen es software libre publicado bajo licencia MIT, pero su uso
implica varios riesgos importantes que el integrador debe conocer y
asumir.

## Documentos clave

- **[License (MIT)](../legal/license.md)** — texto completo de la
  licencia. Resumen: puedes usar, copiar, modificar, distribuir libre
  y comercialmente. Mantén el aviso de copyright + esta licencia.
- **[Disclaimer](../legal/disclaimer.md)** — riesgos antes de usar.
  WhatsApp ToS, GDPR, limitación de responsabilidad.
- **[Notice](../legal/notice.md)** — atribución a librerías de
  terceros (whatsmeow, Echo, pgx, etc.) y a marcas registradas
  (WhatsApp es propiedad de Meta).

## Aviso importante

qrsgen **no está afiliado** con WhatsApp ni Meta. Usa una API no
oficial obtenida por ingeniería inversa via la librería whatsmeow. Esto
implica:

- **Riesgo de baneo del número**: WhatsApp puede penalizar o bloquear
  números que detecte como clientes no oficiales. El BanWatcher reduce
  el riesgo pero no lo elimina.
- **Sin SLA**: WhatsApp puede romper el protocolo en cualquier momento.
  whatsmeow se actualiza, pero a veces hay ventanas de incompatibilidad.
- **GDPR / compliance**: si procesas datos de ciudadanos UE, eres
  responsable del cumplimiento. qrsgen registra metadatos (JIDs,
  contenidos) en Postgres — debes tratar esos datos como PII.
- **WhatsApp Terms of Service**: el uso de APIs no oficiales puede
  violar los ToS. Léelos antes de desplegar en un entorno comercial
  serio.

Para uso comercial intensivo o regulado, considera **WhatsApp Cloud
API** (oficial, de pago, sin riesgo de ban) en lugar de qrsgen.

## Limitación de responsabilidad

Bajo la licencia MIT, el software se proporciona "tal cual" sin
garantías. Los autores y contribuidores no son responsables de baneos,
pérdida de datos, sanciones regulatorias ni daños derivados del uso.
Lee el [Disclaimer](../legal/disclaimer.md) completo antes de usar.
