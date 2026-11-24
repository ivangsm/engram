package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAuthMiddleware(t *testing.T) {
	token := "secret-test-token"
	app := fiber.New()
	app.Use(New(token))
	app.Get("/protected", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	// 1. Missing token -> 401
	req := httptest.NewRequest("GET", "/protected", nil)
	resp, err := app.Test(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}

	// 2. Invalid token -> 403
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	resp, err = app.Test(req)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", resp.StatusCode)
	}

	// 3. Valid Bearer header -> 200
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = app.Test(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 4. Valid Query param token (?token=...) -> 200
	req = httptest.NewRequest("GET", "/protected?token="+token, nil)
	resp, err = app.Test(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK with query token, got %d", resp.StatusCode)
	}

	// 5. Unprotected health path -> 200 even without token
	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	req = httptest.NewRequest("GET", "/healthz", nil)
	resp, err = app.Test(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for /healthz, got %d", resp.StatusCode)
	}
}
