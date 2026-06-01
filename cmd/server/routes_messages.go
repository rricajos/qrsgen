package main

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/rricajos/qrsgen/internal/manager"
)

// registerMessageRoutes monta endpoints relacionados con manipulación
// de mensajes individuales. Por ahora sólo edit; futuros candidatos:
// delete (revoke), forward, react-on-behalf. v0.60.0.
func registerMessageRoutes(api *echo.Group, mgr *manager.Manager) {
	// POST /api/instances/:name/messages/:waid/edit (v0.60.0)
	//
	// Edita el contenido de un mensaje saliente previamente enviado.
	// Body: {"chat":"<jid>", "content":"new text"}.
	//
	// Restricciones de WhatsApp:
	//   - El mensaje debe ser saliente (fromMe).
	//   - Hay una ventana temporal (~15 min) tras la cual la edición
	//     se rechaza por el server.
	//   - El cliente del destinatario debe estar online para aplicar.
	//
	// Respuesta 200: {"waid":"<same>", "edited":true}.
	api.POST("/instances/:name/messages/:waid/edit", func(c echo.Context) error {
		instance := c.Param("name")
		waid := c.Param("waid")
		if waid == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "waid required"})
		}
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		var body struct {
			Chat    string `json:"chat"`
			Content string `json:"content"`
		}
		if err := c.Bind(&body); err != nil || body.Chat == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "chat + content required"})
		}
		if body.Content == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "empty content not allowed; use DELETE to revoke instead"})
		}
		newWAID, err := conn.EditMessage(c.Request().Context(), body.Chat, waid, body.Content)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"waid":   newWAID,
			"edited": true,
		})
	})
}
