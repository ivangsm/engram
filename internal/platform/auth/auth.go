package auth

import (
	"crypto/subtle"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Config holds the authentication configuration.
type Config struct {
	BearerToken string
	HeaderName  string
}

// New creates a Fiber middleware that enforces Bearer token authentication.
func New(token string) fiber.Handler {
	expectedToken := []byte(token)

	return func(c *fiber.Ctx) error {
		// Allow health/ping endpoints without auth
		path := c.Path()
		if path == "/healthz" || path == "/livez" || path == "/readyz" {
			return c.Next()
		}

		if len(expectedToken) == 0 {
			// If no token is configured, fail closed
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"type":   "https://engram.dev/errors/unauthorized",
				"title":  "Unauthorized",
				"status": fiber.StatusUnauthorized,
				"detail": "Server has no auth token configured",
			})
		}

		var provided string

		// 1. Check Authorization header
		authHeader := c.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				provided = parts[1]
			}
		}

		// 2. Fallback to ?token= query parameter (essential for EventSource/SSE)
		if provided == "" {
			provided = c.Query("token")
		}

		// 3. Fallback to X-API-Key header
		if provided == "" {
			provided = c.Get("X-API-Key")
		}

		if provided == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"type":   "https://engram.dev/errors/unauthorized",
				"title":  "Unauthorized",
				"status": fiber.StatusUnauthorized,
				"detail": "Missing bearer token in Authorization header or token query parameter",
			})
		}

		if subtle.ConstantTimeCompare([]byte(provided), expectedToken) != 1 {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"type":   "https://engram.dev/errors/forbidden",
				"title":  "Forbidden",
				"status": fiber.StatusForbidden,
				"detail": "Invalid bearer token",
			})
		}

		return c.Next()
	}
}
