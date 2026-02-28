package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// BasicAuth returns middleware that protects routes with HTTP Basic Authentication.
func BasicAuth(login, password string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Basic ") {
			c.Set("WWW-Authenticate", `Basic realm="admin"`)
			return c.SendStatus(fiber.StatusUnauthorized)
		}

		decoded, err := base64.StdEncoding.DecodeString(auth[6:])
		if err != nil {
			c.Set("WWW-Authenticate", `Basic realm="admin"`)
			return c.SendStatus(fiber.StatusUnauthorized)
		}

		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 ||
			subtle.ConstantTimeCompare([]byte(parts[0]), []byte(login)) != 1 ||
			subtle.ConstantTimeCompare([]byte(parts[1]), []byte(password)) != 1 {
			c.Set("WWW-Authenticate", `Basic realm="admin"`)
			return c.SendStatus(fiber.StatusUnauthorized)
		}

		return c.Next()
	}
}

// AdminPage serves the admin dashboard.
func (h *Handlers) AdminPage(c *fiber.Ctx) error {
	return c.SendFile("./web/admin.html")
}

// AdminGetUsers returns all users as JSON.
func (h *Handlers) AdminGetUsers(c *fiber.Ctx) error {
	users, err := h.DB.GetAllUsers(context.Background())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load users"})
	}
	if users == nil {
		return c.JSON([]struct{}{})
	}
	return c.JSON(users)
}

// AdminGetMonitors returns all monitors as JSON (full details for admin).
func (h *Handlers) AdminGetMonitors(c *fiber.Ctx) error {
	monitors, err := h.DB.GetAllMonitors(context.Background())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load monitors"})
	}
	if monitors == nil {
		return c.JSON([]struct{}{})
	}
	return c.JSON(monitors)
}
