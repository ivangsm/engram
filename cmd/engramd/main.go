package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/ivan/engram/internal/core/memories"
	"github.com/ivan/engram/internal/core/projects"
	"github.com/ivan/engram/internal/core/state"
	"github.com/ivan/engram/internal/mcp"
	"github.com/ivan/engram/internal/platform/auth"
	platformdb "github.com/ivan/engram/internal/platform/db"
	"github.com/ivan/engram/internal/platform/embeddings"
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}

func main() {
	port := getEnvInt("ENGRAM_PORT", 8085)
	dbHost := getEnv("ENGRAM_DB_HOST", "localhost")
	dbPort := getEnvInt("ENGRAM_DB_PORT", 5439)
	dbUser := getEnv("ENGRAM_DB_USER", "engram")
	dbPass := getEnv("ENGRAM_DB_PASSWORD", "engram_dev_secret")
	dbName := getEnv("ENGRAM_DB_NAME", "engram")
	dbSSL := getEnv("ENGRAM_DB_SSLMODE", "disable")
	authToken := getEnv("ENGRAM_AUTH_TOKEN", "engram_dev_token_2026")
	migrationsDir := getEnv("ENGRAM_MIGRATIONS_DIR", "migrations")

	log.Printf("[engramd] Starting Engram daemon on :%d (DB %s:%d/%s)", port, dbHost, dbPort, dbName)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Connect to PostgreSQL
	pool, err := platformdb.NewPool(ctx, platformdb.Config{
		Host:     dbHost,
		Port:     dbPort,
		User:     dbUser,
		Password: dbPass,
		Database: dbName,
		SSLMode:  dbSSL,
	})
	if err != nil {
		log.Printf("[engramd] Warning: failed to connect to Postgres pool: %v", err)
		log.Printf("[engramd] Server will run in degraded mode waiting for database.")
	} else {
		defer pool.Close()
		log.Printf("[engramd] Connected to PostgreSQL 17 successfully")

		// Run migrations if directory exists
		if _, err := os.Stat(migrationsDir); err == nil {
			if migErr := platformdb.ApplyMigrations(ctx, pool, migrationsDir); migErr != nil {
				log.Printf("[engramd] Warning: migrations execution: %v", migErr)
			} else {
				log.Printf("[engramd] Database schema migrations applied successfully")
			}
		}
	}

	// 2. Initialize Core Services
	var memSvc *memories.Service
	var stateSvc *state.Service
	var projSvc *projects.Service
	var mcpServer *mcp.Server
	var outboxListener *platformdb.OutboxListener

	embedder, err := embeddings.NewFromEnv()
	if err != nil {
		log.Fatalf("[engramd] Embedding provider configuration error: %v", err)
	}
	if embedder != nil {
		log.Printf("[engramd] Embeddings provider enabled: %s", embedder.Name())
	}

	if pool != nil {
		memSvc = memories.NewServiceWithEmbedder(pool, embedder)
		stateSvc = state.NewService(pool)
		projSvc = projects.NewService(pool)
		mcpRegistry := mcp.NewRegistry(memSvc, stateSvc, projSvc)
		mcpServer = mcp.NewServer(mcpRegistry)

		dbCfg := platformdb.Config{
			Host:     dbHost,
			Port:     dbPort,
			User:     dbUser,
			Password: dbPass,
			Database: dbName,
			SSLMode:  dbSSL,
		}
		outboxListener = platformdb.NewOutboxListener(pool, dbCfg.DSN())
		if err := outboxListener.Start(ctx); err != nil {
			log.Printf("[engramd] Warning: failed to start outbox listener: %v", err)
		} else {
			mcpServer.SetEventListener(outboxListener)
		}
	}

	// 3. Setup Fiber Application
	app := fiber.New(fiber.Config{
		AppName:               "Engram Protocol Server v0.1.0",
		DisableStartupMessage: false,
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "[${time}] ${status} - ${latency} ${method} ${path}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-API-Key",
		AllowMethods: "GET, POST, HEAD, OPTIONS",
	}))

	// Health probes
	app.Get("/healthz", func(c *fiber.Ctx) error {
		if pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "degraded", "db": "disconnected"})
		}
		if err := pool.Ping(c.UserContext()); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "degraded", "db": err.Error()})
		}
		return c.JSON(fiber.Map{"status": "healthy", "service": "engramd"})
	})

	// 4. Mount Huma v2 OpenAPI router
	humaConfig := huma.DefaultConfig("Engram API", "0.1.0")
	humaConfig.DocsPath = "/docs"
	api := humafiber.NewV2(app, humaConfig)

	type HealthOutput struct {
		Body struct {
			Status    string `json:"status" example:"ok"`
			Timestamp string `json:"timestamp"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/api/v1/health",
		Summary:     "Health check",
	}, func(ctx context.Context, input *struct{}) (*HealthOutput, error) {
		resp := &HealthOutput{}
		resp.Body.Status = "ok"
		resp.Body.Timestamp = time.Now().UTC().Format(time.RFC3339)
		return resp, nil
	})

	// 5. Mount Authenticated MCP Routes
	mcpGroup := app.Group("", auth.New(authToken))
	if mcpServer != nil {
		mcpServer.RegisterRoutes(mcpGroup)
		log.Printf("[engramd] MCP SSE mounted on /sse and /mcp/sse (Bearer Auth Protected)")
		log.Printf("[engramd] MCP RPC mounted on /mcp/message and /mcp/rpc")
	}

	// 6. Graceful Shutdown listener
	go func() {
		addr := fmt.Sprintf(":%d", port)
		if err := app.Listen(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[engramd] Listen error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("[engramd] Shutting down server gracefully...")
	if err := app.ShutdownWithTimeout(5 * time.Second); err != nil {
		log.Printf("[engramd] Error during shutdown: %v", err)
	}
	log.Printf("[engramd] Server stopped")
}
