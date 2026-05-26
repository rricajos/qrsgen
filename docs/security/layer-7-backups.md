# Capa 7 — Backups Postgres

## Qué hace

Un systemd timer (`qrsgen-postgres-backup.timer`) ejecuta `pg_dump -Fc`
de la DB `bridge` cada día a las 03:00 local. Layout en
`/opt/qrsgen-stack/backups/`:

```
daily/    qrsgen-bridge-YYYYMMDD-HHMM.dump     (retención 7 días)
weekly/   qrsgen-bridge-YYYY-WW.dump           (retención 4 semanas, copia los domingos)
```

El timer ejecuta como root (necesita acceso a docker socket). Logs en
`journalctl -u qrsgen-postgres-backup.service`.

## Qué mitiga

Pérdida o corrupción de la DB:

- Disco del VPS dañado.
- DROP TABLE accidental (incluido el audit log — recuerda: el trigger
  bloquea UPDATE/DELETE de **filas**, no la tabla entera).
- Migración fallida que deja la DB en estado inconsistente.

## Cómo verificarla

```bash
# Disparar backup manual:
sudo systemctl start qrsgen-postgres-backup.service
sudo journalctl -u qrsgen-postgres-backup.service -n 20

# Verificar dump:
ls -lh /opt/qrsgen-stack/backups/daily/

# Probar restore en un DB de pruebas (NO en la prod):
docker exec -i postgres pg_restore -l < latest.dump | head -20
```

Runbook de restore completo:
[`ops/backup/README.md`](https://github.com/rricajos/qrsgen/blob/main/ops/backup/README.md).

## Limitación

Los backups están **en el mismo VPS**. Si el VPS se quema, se pierden.
Para producción crítica, configura un `ExecStartPost=` en el `.service`
que pushee el dump a S3/Backblaze/Wasabi.

## Glosario

**Backup**: copia de seguridad de los datos. Para qrsgen son dumps
periódicos de la base de datos `bridge`.

**pg_dump**: utilidad oficial de Postgres para exportar una base de
datos a un fichero. qrsgen usa el formato custom `-Fc` (binario,
comprimido).

**Custom format (`-Fc`)**: formato binario de pg_dump que permite
restore selectivo (solo una tabla, solo el schema, etc.) y es mucho
más rápido que SQL plano.

**systemd timer**: equivalente moderno a cron en Linux con journald.
Lanza un `.service` en horario calendario. qrsgen lo usa para backup
diario a las 03:00.

**Retención**: política sobre cuánto tiempo mantener cada backup
generado. qrsgen tiene 7 días daily + 4 semanas weekly (rotación
dominical) configurables vía drop-in.

**Off-site backup**: copia almacenada en una infraestructura distinta
al servidor primario. Protege contra pérdida del VPS entero (fuego,
borrado accidental por el provider, etc.).

**Drop-in**: archivo `.conf` en `/etc/systemd/system/<unit>.d/` que
extiende o modifica una unit systemd sin tocar el archivo original.
Útil para personalizar `ExecStartPost=`.

**ExecStartPost**: hook de systemd que ejecuta un comando después de
que el `ExecStart` principal termine con éxito. Aquí pushea el dump a
S3/Backblaze/Wasabi.

**Restore selectivo**: capacidad de restaurar solo una tabla o un
schema concreto del backup. Requiere formato custom (`-Fc`).
