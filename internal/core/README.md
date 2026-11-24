# core

## Overview
Domain business logic layer for Engram encapsulating persistent semantic knowledge, cryptographic provenance, and concurrent operational state coordination.

## Subdirectories & Boundaries
- [memories](./memories/README.md): Semantic memory manifests, on-demand hydration, dual-tier hybrid search, SHA-256 cryptographic chaining, unified diff generation, and strict Optimistic Concurrency Control (OCC).
- [state](./state/README.md): Operational task queueing, atomic row-level lease acquisition (`FOR UPDATE SKIP LOCKED`), lease fencing tokens (`lease_id`), poison-pill starvation protection, and structured cross-agent handoffs.
