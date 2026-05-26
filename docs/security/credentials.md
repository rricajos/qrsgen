# Credenciales en orquestadores externos

Los workflows del orquestador (n8n u otros) deben usar el **mecanismo de
credenciales propio** del orquestador, no hardcodear tokens en el JSON.

En n8n:

- Downstream API → `httpHeaderAuth` con header `api_access_token`.
- qrsgen API → `httpHeaderAuth` con header `Authorization: Bearer ...`.

Los credentials se guardan encriptados en la DB de n8n.
`n8n export:workflow` incluye solo los IDs de credentials, no los valores.

→ Mitiga el riesgo de filtrar secretos al hacer screenshot/export de
workflows.
