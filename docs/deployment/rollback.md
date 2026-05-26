# Rollback

Si el nuevo tag se porta mal, vuelve al anterior cambiando solo el `.env`:

```bash
sed -i 's/^QRSGEN_VERSION=.*/QRSGEN_VERSION=0.21.0/' /opt/qrsgen-stack/.env
docker stack deploy -c /opt/qrsgen-stack/docker-compose.yml --resolve-image=changed qrsgen
```

~30s y vuelves al estado anterior. Las migraciones nuevas son aditivas
(columnas y tablas nuevas no rompen versiones viejas), así que el
rollback es seguro.

Ver [Schema migrations — compatibilidad backwards](schema-migrations.md)
para el detalle.
