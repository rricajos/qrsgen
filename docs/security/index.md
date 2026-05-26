# Seguridad — Visión general

qrsgen se diseña con **defense-in-depth**: ninguna capa es suficiente por
sí sola, pero la combinación cubre los tres vectores más probables.

## Modelo de amenaza

qrsgen vive en una overlay docker compartida con el downstream y,
típicamente, un orquestador como n8n. Asumimos tres vectores principales:

1. **Compromiso lateral en LAN** — otro container del overlay queda
   vulnerado. ¿Qué puede hacer contra qrsgen?
2. **Compromiso del propio qrsgen** — un atacante gana RCE en el proceso.
   ¿Qué puede exfiltrar o usar como pivote?
3. **MITM en el WebSocket WhatsApp** — un atacante con acceso al host o
   a la red, ¿puede interceptar el tráfico hacia Meta?

Las siete capas mitigan estos vectores en distinta medida. Cada capa
documenta **qué hace**, **cómo configurarla**, **qué mitiga** y **cómo
verificarla**.

## Capas

1. [Bearer auth de la API](layer-1-bearer.md)
2. [HMAC del webhook entrante](layer-2-hmac.md)
3. [Egress firewall (iptables)](layer-3-firewall.md)
4. [TLS WhatsApp](layer-4-tls.md)
5. [Container hardening](layer-5-hardening.md)
6. [Audit log inmutable](layer-6-audit.md)
7. [Backups Postgres](layer-7-backups.md)

## Material complementario

- [Credenciales en orquestadores externos](credentials.md)
- [Observabilidad y logs](observability.md)
- [Mejoras pendientes](pending.md)
