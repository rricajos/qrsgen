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

## Glosario

**Rollback**: volver a una versión anterior del software tras detectar
un problema con la nueva.

**Backwards compatible**: una versión nueva que respeta el formato de
datos de las versiones anteriores. qrsgen lo respeta con migraciones
aditivas.

**Aditivo (schema)**: cambios que solo añaden tablas/columnas, sin
modificar las existentes. Esto garantiza que un binario antiguo siga
leyendo el schema sin fallos.

**Pin de versión**: especificar exactamente qué versión usar (e.g.
`QRSGEN_VERSION=0.21.0` en `.env`) en lugar de `:latest` que se mueve.

**Tag immutable**: cada tag `v0.23.0` apunta siempre al mismo binario.
qrsgen sigue esa convención — no se reescriben tags publicados.

**Redeploy**: lanzar `docker stack deploy` de nuevo con la config
actualizada. Docker hace el rollout según el `update_config`.
