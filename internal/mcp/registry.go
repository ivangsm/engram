package mcp

import (
	"github.com/ivan/engram/internal/core/memories"
	"github.com/ivan/engram/internal/core/projects"
	"github.com/ivan/engram/internal/core/state"
)

type Registry struct {
	memoriesSvc *memories.Service
	stateSvc    *state.Service
	projectsSvc *projects.Service
}

func NewRegistry(m *memories.Service, s *state.Service, p *projects.Service) *Registry {
	return &Registry{
		memoriesSvc: m,
		stateSvc:    s,
		projectsSvc: p,
	}
}
