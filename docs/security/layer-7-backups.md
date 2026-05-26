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
