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

## Glosario

**Credentials encriptadas**: secrets (tokens, passwords) almacenados
cifrados en la DB del orquestador, no en plaintext en JSONs de
workflow.

**Hardcoded secret**: token o password escrito directamente en el
código/JSON. Mala práctica — se filtra fácil en exports, screenshots,
historial git.

**httpHeaderAuth (n8n)**: tipo de credencial en n8n que añade un
header HTTP arbitrario a cada request. Usado tanto para
`api_access_token` (downstream) como para `Authorization: Bearer`
(qrsgen).

**Export de workflow**: serialización a JSON del workflow para
backup, compartir o versionar. En n8n, `n8n export:workflow` solo
incluye **IDs** de credentials, no sus valores.

**Screenshot leakage**: cuando alguien hace una captura de pantalla
para soporte y sin querer expone un token. Con credentials
encriptadas, el screenshot solo muestra el nombre de la credencial.
