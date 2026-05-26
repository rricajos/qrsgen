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

## Glosario

**TLS** (Transport Layer Security): protocolo que cifra el tráfico TCP.
Sucesor de SSL. WhatsApp Web lo usa para todo el tráfico cliente-Meta.

**WebSocket sobre TLS** (`wss://`): conexión WebSocket cifrada. qrsgen
mantiene una por instancia contra Meta.

**MITM pasivo**: atacante en la red que solo lee tráfico (sniff). TLS
lo previene — solo ve ciphertext.

**MITM activo**: atacante que se interpone modificando tráfico. TLS lo
previene **si el cert es válido** (no firmado por una CA controlada
por el atacante).

**CA** (Certificate Authority): entidad que firma certificados TLS.
El sistema operativo y las imágenes Docker confían en una lista
preinstalada (Let's Encrypt, DigiCert, etc.).

**CA root compromise**: situación en la que un atacante mete una CA
maliciosa en el trust store del host. Permitiría MITM activo. Requiere
root del host — si lo tiene, ya estás perdido por otras vías.

**Cert pinning**: técnica donde el cliente solo confía en certs con un
fingerprint específico, no en la cadena CA. Defensa contra CA root
compromise. Alto mantenimiento porque los certs rotan.

**Bundle de CAs**: archivo con todas las CAs en las que el OS confía.
La imagen distroless de qrsgen lo lleva preinstalado.

**Certificado de Meta**: cert TLS que los servidores de WhatsApp
presentan. Lo firma una CA pública estándar.
