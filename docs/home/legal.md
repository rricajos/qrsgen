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

## Glosario

**Licencia MIT**: licencia de software permisiva — permite uso, copia,
modificación y distribución libre y comercial. Solo exige mantener el
aviso de copyright + el texto de la licencia.

**API no oficial**: API reverse-engineered, no documentada ni
soportada por el proveedor. qrsgen usa una vía whatsmeow contra Meta.

**Ingeniería inversa**: proceso de analizar un protocolo binario para
implementarlo sin documentación oficial. whatsmeow lo hace para
WhatsApp Web.

**ToS** (Terms of Service): términos de uso del proveedor. Los de
WhatsApp pueden prohibir clientes no oficiales — léelos antes de
desplegar en producción.

**GDPR** (General Data Protection Regulation): regulación europea de
protección de datos. Aplica si procesas datos de ciudadanos UE —
incluyendo metadatos como JIDs y contenido de mensajes.

**PII** (Personally Identifiable Information): datos que identifican a
una persona concreta. Los JIDs/teléfonos/mensajes son PII bajo GDPR.

**SLA** (Service Level Agreement): acuerdo de nivel de servicio con
compromisos formales de disponibilidad. WhatsApp Web NO tiene SLA;
WhatsApp Cloud API sí (es de pago).

**WhatsApp Cloud API**: API oficial de WhatsApp Business gestionada por
Meta. De pago, con SLA, sin riesgo de ban — alternativa a qrsgen para
producción comercial regulada.

**Riesgo de ban**: probabilidad de que WhatsApp restrinja o bloquee un
número detectado como cliente no oficial. qrsgen lo mitiga con
BanWatcher pero no lo elimina.

**Limitación de responsabilidad**: cláusula MIT estándar — el software
se entrega "AS IS" sin garantías y los autores no responden por daños
derivados del uso.
