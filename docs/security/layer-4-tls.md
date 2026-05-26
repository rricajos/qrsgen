# Capa 4 — TLS WhatsApp

## Qué hace

whatsmeow usa el cliente TCP/TLS estándar de Go. El bundle de CAs de la
imagen distroless valida los certificados de Meta. **MITM pasivo es
imposible** (TLS estricto).

## Qué mitiga

Vector #3 (MITM): un atacante en la red que intente leer el tráfico
qrsgen ↔ Meta solo verá ciphertext.

## Limitaciones

MITM **activo** requeriría:

- Comprometer una CA root del VPS (requiere root del host).
- Forzar al cliente a aceptar un cert arbitrario (whatsmeow no lo permite
  sin patches).

Sin **cert pinning** explícito, un atacante con root del VPS podría
inyectar una CA root y MITM. Pero si tienes root del VPS comprometido, el
MITM del WebSocket es la menor de tus preocupaciones — pueden simplemente
leer la memoria del proceso.

## Mejora futura

Cert pinning en whatsmeow para defender ante CA root compromise (alto
esfuerzo de mantenimiento — los certs de Meta rotan).

## Cómo verificarla

```bash
# Capturar tráfico saliente; debe ser todo TLS hacia *.whatsapp.net
sudo tcpdump -i any -nn 'host whatsapp.net' -c 5
```
