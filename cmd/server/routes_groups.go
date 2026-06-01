package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"go.mau.fi/whatsmeow/types"

	"github.com/rricajos/qrsgen/internal/manager"
)

// registerGroupRoutes monta los endpoints HTTP de administración de
// grupos WhatsApp bajo `api`. Todos requieren que la instancia esté
// registrada en `mgr` y devuelven 404 si no es así. Aislados aquí
// (v0.54.2) para evitar inflar main.go — el dominio "grupos" es
// completamente self-contained, sólo depende de wameow.Conn.GroupX.
func registerGroupRoutes(api *echo.Group, mgr *manager.Manager) {
	// GET /api/instances/:name/groups/:jid
	// v0.48.0: información del grupo (subject, topic, settings,
	// participantes con sus roles). Round-trip al server WA.
	api.GET("/instances/:name/groups/:jid", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid jid: " + err.Error()})
		}
		if jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "jid must be a group (@g.us)"})
		}
		info, err := conn.GroupInfo(c.Request().Context(), jid)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, info)
	})

	// POST /api/instances/:name/groups/:jid/name
	// v0.48.0: cambia el nombre (subject) del grupo. Body: {"name":"X"}.
	// Requiere que el bot sea admin del grupo.
	api.POST("/instances/:name/groups/:jid/name", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name required"})
		}
		if err := conn.SetGroupName(c.Request().Context(), jid, body.Name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "name": body.Name})
	})

	// POST /api/instances/:name/groups/:jid/participants
	// v0.48.0: añadir/expulsar/promover/degradar miembros del grupo.
	// Body: {"action":"add|remove|promote|demote", "jids":["34...@s.whatsapp.net", ...]}.
	// Requiere que el bot sea admin.
	api.POST("/instances/:name/groups/:jid/participants", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Action string   `json:"action"`
			JIDs   []string `json:"jids"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if len(body.JIDs) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "jids required"})
		}
		parsed := make([]types.JID, 0, len(body.JIDs))
		for _, s := range body.JIDs {
			p, err := types.ParseJID(s)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid participant jid: " + s})
			}
			parsed = append(parsed, p)
		}
		if err := conn.UpdateGroupParticipants(c.Request().Context(), jid, body.Action, parsed); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"jid":    jid.String(),
			"action": body.Action,
			"count":  len(parsed),
		})
	})

	// POST /api/instances/:name/groups/:jid/topic
	// v0.50.0: cambia el topic (descripción) del grupo. Body: {"topic":"X"}.
	// topic vacío = quitar descripción. Requiere bot admin.
	api.POST("/instances/:name/groups/:jid/topic", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Topic string `json:"topic"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if err := conn.SetGroupTopic(c.Request().Context(), jid, body.Topic); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "topic": body.Topic})
	})

	// POST /api/instances/:name/groups/:jid/locked
	// v0.50.0: toggle "solo admins editan info del grupo".
	// Body: {"locked": true|false}. Requiere bot admin.
	api.POST("/instances/:name/groups/:jid/locked", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Locked bool `json:"locked"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if err := conn.SetGroupLocked(c.Request().Context(), jid, body.Locked); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "locked": body.Locked})
	})

	// POST /api/instances/:name/groups/:jid/announce
	// v0.50.0: toggle "modo anuncio" (solo admins envían msgs).
	// Body: {"announce": true|false}. Requiere bot admin.
	api.POST("/instances/:name/groups/:jid/announce", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		var body struct {
			Announce bool `json:"announce"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		if err := conn.SetGroupAnnounce(c.Request().Context(), jid, body.Announce); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "announce": body.Announce})
	})

	// POST /api/instances/:name/groups
	// v0.50.0: crear grupo nuevo. Body: {"name": "X", "participants":
	// ["34...@s.whatsapp.net", ...]}. Max 25 chars en name.
	api.POST("/instances/:name/groups", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		var body struct {
			Name         string   `json:"name"`
			Participants []string `json:"participants"`
		}
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name required"})
		}
		if len(body.Participants) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "participants required"})
		}
		parsed := make([]types.JID, 0, len(body.Participants))
		for _, s := range body.Participants {
			p, err := types.ParseJID(s)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid participant: " + s})
			}
			parsed = append(parsed, p)
		}
		groupJID, err := conn.CreateGroup(c.Request().Context(), body.Name, parsed)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]any{
			"jid":  groupJID,
			"name": body.Name,
		})
	})

	// DELETE /api/instances/:name/groups/:jid
	// v0.50.0: el bot abandona el grupo.
	api.DELETE("/instances/:name/groups/:jid", func(c echo.Context) error {
		instance := c.Param("name")
		conn, ok := mgr.Get(instance)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
		}
		jid, err := types.ParseJID(c.Param("jid"))
		if err != nil || jid.Server != types.GroupServer {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group jid"})
		}
		if err := conn.LeaveGroup(c.Request().Context(), jid); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"jid": jid.String(), "left": true})
	})
}
