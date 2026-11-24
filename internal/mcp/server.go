package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

type EventListener interface {
	Subscribe(projectID string) (<-chan []byte, func())
}

type Server struct {
	registry      *Registry
	eventListener EventListener
	sessions      sync.Map // map[string]chan []byte
}

func NewServer(registry *Registry) *Server {
	return &Server{
		registry: registry,
	}
}

func (s *Server) SetEventListener(l EventListener) {
	s.eventListener = l
}

// RegisterRoutes mounts the MCP SSE and RPC endpoints onto a Fiber router.
func (s *Server) RegisterRoutes(app fiber.Router) {
	app.Get("/sse", s.handleSSE)
	app.Get("/mcp/sse", s.handleSSE)
	app.Post("/mcp/message", s.handleMessage)
	app.Post("/mcp/rpc", s.handleMessage)
}

func (s *Server) handleSSE(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")
	c.Set("Access-Control-Allow-Origin", "*")

	sessionID := uuid.New().String()
	msgChan := make(chan []byte, 32)
	s.sessions.Store(sessionID, msgChan)

	var subChan <-chan []byte
	var unsub func()
	if s.eventListener != nil {
		projectID := c.Query("project_id", "")
		subChan, unsub = s.eventListener.Subscribe(projectID)
	}

	c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		defer func() {
			if unsub != nil {
				unsub()
			}
			s.sessions.Delete(sessionID)
			close(msgChan)
		}()

		// Send endpoint discovery event
		endpointURI := fmt.Sprintf("/mcp/message?sessionId=%s", sessionID)
		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURI)
		if err := w.Flush(); err != nil {
			return
		}

		for {
			select {
			case msg, ok := <-msgChan:
				if !ok {
					return
				}
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(msg))
				if err := w.Flush(); err != nil {
					return
				}
			case ev, ok := <-subChan:
				if !ok {
					return
				}
				fmt.Fprintf(w, "event: engram_event\ndata: %s\n\n", string(ev))
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	}))

	return nil
}

func (s *Server) handleMessage(c *fiber.Ctx) error {
	sessionID := c.Query("sessionId")
	body := c.Body()

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		resp := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &RPCError{
				Code:    ParseError,
				Message: "Parse error: " + err.Error(),
			},
		}
		return c.Status(fiber.StatusBadRequest).JSON(resp)
	}

	res, err := s.processRPC(c.UserContext(), req)
	if err != nil {
		resp := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    InternalError,
				Message: err.Error(),
			},
		}
		return c.Status(fiber.StatusInternalServerError).JSON(resp)
	}

	// Notifications in JSON-RPC do not require a response payload
	if req.ID == nil && req.Method == "notifications/initialized" {
		return c.SendStatus(fiber.StatusNoContent)
	}

	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  res,
	}

	// If sessionID is associated with an active SSE stream, also mirror event there
	if sessionID != "" {
		if chVal, ok := s.sessions.Load(sessionID); ok {
			ch := chVal.(chan []byte)
			data, _ := json.Marshal(resp)
			select {
			case ch <- data:
			default:
			}
		}
	}

	return c.JSON(resp)
}

func (s *Server) processRPC(ctx context.Context, req JSONRPCRequest) (interface{}, error) {
	switch req.Method {
	case "initialize":
		return InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: ServerCapabilities{
				Tools: &ToolsCapability{
					ListChanged: false,
				},
			},
			ServerInfo: Implementation{
				Name:    "engram",
				Version: "0.1.0",
			},
		}, nil

	case "notifications/initialized":
		return nil, nil

	case "ping":
		return map[string]interface{}{}, nil

	case "tools/list":
		return ToolsListResult{
			Tools: s.registry.ListTools(),
		}, nil

	case "tools/call":
		var callReq CallToolRequest
		if err := json.Unmarshal(req.Params, &callReq); err != nil {
			return nil, fmt.Errorf("invalid tools/call params: %w", err)
		}
		return s.registry.ExecuteTool(ctx, callReq.Name, callReq.Arguments)

	default:
		return nil, fmt.Errorf("method not found: %s", req.Method)
	}
}
