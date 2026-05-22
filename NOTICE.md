# Third-party dependencies

qrsgen incorpora código y librerías de terceros bajo sus respectivas licencias. Esta no es una lista exhaustiva — ver `go.mod` y `go.sum` para el árbol completo de dependencias.

## Dependencias principales

| Dependencia | Licencia | Propósito |
|---|---|---|
| [whatsmeow](https://github.com/tulir/whatsmeow) | MPL-2.0 | Cliente WhatsApp Multi-Device (ingeniería inversa, no oficial) |
| [Echo v4](https://github.com/labstack/echo) | MIT | HTTP framework |
| [pgx/v5](https://github.com/jackc/pgx) | MIT | Driver Postgres |
| [go-qrcode](https://github.com/skip2/go-qrcode) | MIT | Generación PNG del QR |
| [caarlos0/env](https://github.com/caarlos0/env) | MIT | Parser de env vars |
| [prometheus/client_golang](https://github.com/prometheus/client_golang) | Apache-2.0 | Métricas Prometheus |
| [Go standard library](https://go.dev/) | BSD-3-Clause | Runtime de Go (slog, net/http, etc.) |

## Marcas registradas

- **WhatsApp**, **WhatsApp Web**, **Meta** son marcas de Meta Platforms, Inc.

Su uso aquí es estrictamente descriptivo (interoperabilidad técnica). qrsgen no está respaldado ni asociado oficialmente con ninguna de estas entidades.

## Licencias de las dependencias incluidas en el binario

Al compilar qrsgen con `go build`, el binario resultante contiene código de todas las dependencias listadas en `go.sum`. Para extraer la lista completa de licencias del binario:

```bash
go install github.com/google/go-licenses@latest
go-licenses report ./cmd/server > LICENSES-bundled.csv
```

Si vas a redistribuir el binario qrsgen, debes acompañarlo con las atribuciones requeridas por cada licencia (especialmente MPL-2.0 de whatsmeow, que requiere mantener accesible el código fuente de las modificaciones a la propia librería).

## Atribuciones obligatorias

### whatsmeow (MPL-2.0)

> This Source Code Form is subject to the terms of the Mozilla Public License,
> v. 2.0. If a copy of the MPL was not distributed with this file, You can
> obtain one at https://mozilla.org/MPL/2.0/.

qrsgen utiliza whatsmeow como dependencia sin modificarlo. Si forkeas y modificas whatsmeow, debes publicar tus modificaciones bajo MPL-2.0.

### prometheus/client_golang (Apache-2.0)

> Copyright (c) The Prometheus Authors
> Licensed under the Apache License, Version 2.0

## Contribución de código

Cualquier contribución al proyecto qrsgen se asume bajo la licencia del propio proyecto (MIT). Los contribuyentes reconocen tener el derecho legal de aportar el código bajo dicha licencia.
