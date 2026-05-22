# Disclaimer

Lee este archivo antes de desplegar o usar qrsgen.

## No afiliación

Este proyecto **no está afiliado, asociado, autorizado, respaldado por, ni de ninguna manera oficialmente conectado con WhatsApp Inc., Meta Platforms Inc., ni con ninguna de sus filiales**.

Los nombres "WhatsApp", "WhatsApp Web", "Meta", "Facebook" y los logos asociados son marcas registradas de sus respectivos propietarios. Su uso aquí se limita a descripción técnica funcional.

## Riesgo de uso de la API no oficial de WhatsApp

qrsgen utiliza la librería [whatsmeow](https://github.com/tulir/whatsmeow), que implementa el protocolo Multi-Device de WhatsApp Web mediante ingeniería inversa. Esto implica:

1. **Posible violación de los WhatsApp Terms of Service**:
   - https://www.whatsapp.com/legal/terms-of-service
   - https://www.whatsapp.com/legal/business-terms
   - Whatsapp prohíbe expresamente el uso "non-personal" sin sus APIs oficiales (Cloud API / Business API).

2. **Riesgo de baneo del número de teléfono**:
   - WhatsApp puede detectar patrones de automatización y banear (temporal o permanentemente) el número conectado.
   - El evento `strike` que emite qrsgen es precisamente eso: una señal de aviso recibida del servidor.
   - No hay garantía alguna contra esto. Si tu negocio depende del número, **úsalo bajo tu propio riesgo**.

3. **Cambios sin previo aviso**:
   - Meta puede cambiar el protocolo en cualquier momento → whatsmeow (y por tanto qrsgen) puede dejar de funcionar.

## Uso recomendado

Para uso **comercial serio**, usa la WhatsApp Cloud API oficial vía un BSP (Business Solution Provider). qrsgen es apropiado para:

- Desarrollo y testing
- Casos de uso internos a tu organización
- Volumenes bajos donde el riesgo de baneo es aceptable
- Como **puente temporal** mientras se completa la migración a Cloud API

## Limitación de responsabilidad

qrsgen se distribuye bajo la licencia MIT, que incluye específicamente:

> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND [...]
> IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM,
> DAMAGES OR OTHER LIABILITY [...]

Los autores y contribuyentes de qrsgen **no son responsables** de:

- Baneo o suspensión del número de WhatsApp del usuario.
- Pérdida de mensajes, contactos, conversaciones o datos derivados.
- Multas, daños o consecuencias derivadas de violaciones de los términos de WhatsApp.
- Multas o sanciones por incumplimiento de regulaciones de protección de datos (GDPR, LOPD-GDD, CCPA, etc.).
- Cualquier otro daño directo, indirecto, incidental, especial o consecuente.

El **operador del despliegue** (quien instale qrsgen y conecte números) es el responsable único de:

- Verificar que su caso de uso cumple con los WhatsApp Terms.
- Obtener los consentimientos GDPR/LOPD necesarios de los usuarios cuyos mensajes pasen por el sistema.
- Implementar las medidas de seguridad necesarias sobre la infraestructura.
- Mantener un Registro de Actividades de Tratamiento como Encargado/Responsable de Tratamiento según aplique.

## Protección de datos (GDPR / LOPD-GDD)

qrsgen procesa **mensajes personales** entre técnicos y clientes finales. Si lo despliegas en jurisdicción EU/EEA:

- Eres **Encargado del Tratamiento** (o Responsable, según tu modelo) sobre los datos que pasen por qrsgen.
- Debes firmar un **DPA (Data Processing Agreement)** con tus clientes si actúas como Encargado.
- Debes informar a los titulares de los datos sobre el subprocesamiento por parte de Meta (WhatsApp).
- Debes mantener los datos cifrados en tránsito y reposo, controles de acceso, logging, derecho de acceso/borrado.

qrsgen como software **no garantiza por sí solo** cumplimiento GDPR — eso depende de tu deployment, configuración y procedimientos.

## Cumplimiento sectorial

Si los mensajes contienen datos sensibles (salud, financieros, menores, etc.), aplican regulaciones adicionales:

- Sanitario en España: RD-Ley 5/2018 + LOPDGDD + normativa autonómica.
- Financiero: PCI-DSS si se procesan tarjetas (NO debería ocurrir vía WhatsApp).

**No uses qrsgen para casos sensibles sin asesoría legal específica.**

## Contribuciones y forks

Si forkeas qrsgen, **mantén este disclaimer en tu fork** y asume tu propia responsabilidad sobre el uso. Los autores originales no respaldan ni se hacen responsables de modificaciones de terceros.
