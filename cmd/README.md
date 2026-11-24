# cmd

## Overview
Entry point directory grouping all standalone executable binary packages for the Engram operational state and memory system.

## Subdirectories & Boundaries
- [engram](./engram/README.md): Developer command-line interface (CLI) communicating with the daemon via JSON-RPC over HTTP.
- [engramd](./engramd/README.md): Central daemon binary managing database connections, outbox event streaming, and MCP protocol transports.
