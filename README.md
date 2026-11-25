# Engram (`engram`)

[![CI](https://github.com/ivangsm/engram/actions/workflows/ci.yml/badge.svg)](https://github.com/ivangsm/engram/actions/workflows/ci.yml)
[![Docker Publish](https://github.com/ivangsm/engram/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/ivangsm/engram/actions/workflows/docker-publish.yml)
[![GitHub Release](https://img.shields.io/github/v/release/ivangsm/engram?include_prereleases)](https://github.com/ivangsm/engram/releases/latest)

> High-density agentic memory service, episodic/semantic persistence engine, and multi-machine operational state plane in Go (Go 1.24+) backed by PostgreSQL 18 with `pgvector` and `pg_trgm`. Exposes standard Model Context Protocol (MCP) streamable HTTP/SSE and a developer CLI.

---

## 1. Architectural Vision: The Dual-Plane Architecture

Traditional autonomous agent workflows fail in two ways:
1. **Context Window Saturation**: Agents dump tens of thousands of tokens of raw Markdown documentation into their context on every prompt, exhausting token budgets and degrading reasoning.
2. **Operational Chaos**: Multi-agent swarms concurrently edit `TODO.md` in flight, resulting in dirty git trees, race conditions, lost task updates, and unverified handoffs.

Engram resolves this by splitting engineering state into two distinct, synchronized planes:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                   GIT REPOSITORY (CANONICAL CANON)                     │
│  - Human-facing architecture docs (docs/, ADRs, specs)                 │
│  - Static source of truth, disaster recovery, cold-start bootstrap     │
│  - Code review in GitHub PRs                                           │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Derivación Semántica & Late Sync
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   ENGRAM STATE PLANE (RUNTIME PLANE)                   │
│  - Progressive disclosure: ~1k token skeletal manifests vs file dumps   │
│  - Atomic task queues & row-level lease fencing (FOR UPDATE SKIP LOCKED)│
│  - Immutable handoffs with reproducible test evidence & git blame      │
│  - Cryptographic blame (SHA-256 chain, exact LLM model, machine ID)    │
└────────────────────────────────────────────────────────────────────────┘
```

### Core Invariants
- **Progressive Disclosure**: Memory listings return compact skeletal manifests (`key`, `digest`, `token_count`, `scope`). Full content hydration occurs strictly on explicit agent demand via `recall_memory`.
- **Strict Optimistic Concurrency Control (OCC)**: Memory updates mandate `expected_parent_hash`. Stale revisions trigger collision rejection with a 3-way unified diff.
- **Cryptographic Blame & Provenance**: Revision records are strictly append-only. Every edit mandates author identification, agent model, machine ID, task ID, rationale, and a SHA-256 hash linked to its parent.
- **Fenced Operational Concurrency**: Task leases use PostgreSQL row-level locks (`FOR UPDATE SKIP LOCKED`) and issue a unique monotonic `lease_id` fencing token. Operations presenting a stale `lease_id` are rejected with `ErrLeaseLost`.
- **Poison-Pill Quarantine**: Tasks exceeding `max_retries = 3` transition to `failed` and record an unalterable autopsy handoff tombstone.
- **Transactional Outbox Event Sourcing**: Every state mutation writes to `engram_events` in the same database transaction and fans out to real-time subscribers via `LISTEN engram_events`.

---

## 2. Quickstart & Deployment

### Opción A: Docker Compose (Recomendado)

El repositorio incluye un `docker-compose.yml` preconfigurado con PostgreSQL 18 (`pgvector`) y el demonio oficial `ghcr.io/ivangsm/engram:latest`:

```bash
# Iniciar stack completo en segundo plano
docker compose up -d

# Verificar estado de los servicios
docker compose ps

# Comprobar probe de salud del demonio
curl -sf http://localhost:8085/healthz
```

### Opción B: Binarios Precompilados (GitHub Releases)

Descarga el release binario oficial para tu arquitectura desde [GitHub Releases](https://github.com/ivangsm/engram/releases/latest):
- **macOS (Apple Silicon)**: `engram_v0.2.0_darwin_arm64.tar.gz`
- **Linux (x86_64)**: `engram_v0.2.0_linux_amd64.tar.gz`
- **Linux (ARM64)**: `engram_v0.2.0_linux_arm64.tar.gz`
- **Windows (x86_64)**: `engram_v0.2.0_windows_amd64.zip`

Descomprime y ubica `engram` y `engramd` en tu `$PATH` (ej. `/usr/local/bin` o `~/.local/bin`).

### Variables de Entorno del Demonio (`engramd`)

| Variable | Por Defecto | Descripción |
| :--- | :--- | :--- |
| `ENGRAM_PORT` | `8085` | Puerto HTTP/SSE del demonio |
| `ENGRAM_DB_HOST` | `localhost` | Host de PostgreSQL 18 |
| `ENGRAM_DB_PORT` | `5439` (Docker) / `5432` | Puerto de PostgreSQL |
| `ENGRAM_DB_USER` | `engram` | Usuario de base de datos |
| `ENGRAM_DB_PASSWORD` | `engram_dev_secret` | Contraseña de base de datos |
| `ENGRAM_DB_NAME` | `engram` | Base de datos principal |
| `ENGRAM_DB_SSLMODE` | `disable` | Modo SSL de conexión (`disable`, `require`) |
| `ENGRAM_AUTH_TOKEN` | `engram_dev_token_2026` | Bearer token requerido para MCP y CLI |
| `ENGRAM_MIGRATIONS_DIR`| `migrations` | Directorio de migraciones SQL |
| `ENGRAM_EMBEDDING_PROVIDER` | `none` | Proveedor opcional de embeddings (`none`, `openai`, `openai-compatible`, `gemini`) |
| `ENGRAM_EMBEDDING_API_KEY` | - | API key del proveedor de embeddings (obligatoria si el proveedor la requiere) |
| `ENGRAM_EMBEDDING_MODEL` | Según proveedor | Modelo de embeddings (`text-embedding-3-small`, `gemini-embedding-001`, etc.) |

---

## 3. Instalación del Plugin Oficial para Antigravity

Engram incluye un plugin empaquetado para Google Antigravity y entornos de agentes AI con soporte completo para reglas permanentes (Dual-Plane), hooks de arranque de sesión y skills de ejecución.

### 1. Instalar el Plugin en el Entorno Local

Puedes enlazar el plugin directamente desde tu copia local del repositorio:

```bash
mkdir -p ~/.gemini/config/plugins
ln -s "$(pwd)/plugins/engram" ~/.gemini/config/plugins/engram
```

O extraerlo directamente desde la imagen oficial de Docker:

```bash
mkdir -p ~/.gemini/config/plugins
docker run --rm -v ~/.gemini/config/plugins:/target ghcr.io/ivangsm/engram:latest cp -r /app/plugins/engram /target/
```

### 2. Contenido del Plugin

El paquete `plugins/engram` incluye de forma preconfigurada:
- **`plugin.json`**: Metadatos y versión del plugin.
- **`mcp_config.json`**: Configuración de transporte MCP HTTP hacia `http://127.0.0.1:8085/mcp/message` con autenticación Bearer (puede removerse si se desea modo CLI-only con cero overhead de tokens de schemas MCP).
- **`rules/AGENTS.md`**: Directivas permanentes de Zero Context Dump, Late Sync y aislamiento del árbol Git.
- **`skills/engram/SKILL.md`**: Runbook ejecutable paso a paso para agentes autónomos.
- **`scripts/session_start.sh`**: Hook `SessionStart` que comprueba la salud del demonio y resuelve automáticamente el proyecto activo.

### 3. Habilitar el Plugin

Antigravity detecta automáticamente los plugins ubicados en `~/.gemini/config/plugins/`. Puedes activarlo mediante el gestor visual de plugins o desde la CLI de Antigravity:

```bash
agy plugin enable engram
```

---

## 4. Inicialización de Proyectos (`engram init`)

El comando `engram init` permite registrar cualquier repositorio en el plano de memoria de Engram, detectando heurísticamente la topología de documentación y generando el archivo local `.engram.json`.

### Ejecución Rápida

Sitúate en la raíz del repositorio que deseas inicializar y ejecuta:

```bash
engram init
```

### Opciones y Flags

```bash
engram init [flags]

Flags:
  --project string   Identificador del proyecto (por defecto: basename del repositorio git)
  --name string      Nombre descriptivo para humanos (por defecto: identificador del proyecto)
  --docs string      Directorio canónico de documentación (ej. "docs" o "checamos-docs")
  --todo string      Archivo Markdown de seguimiento de tareas (por defecto: "TODO.md")
```

### Detección Heurística de Topologías
- **Topología Centralizada**: Si el proyecto tiene directorios como `docs/`, `doc/` o `<proyecto>-docs/`, `engram init` los identifica como la raíz canónica de documentación (`docs_root`).
- **Topología Distribuida**: Si el proyecto contiene archivos Markdown dispersos en la raíz (`README.md`, `ARCHITECTURE.md`, `DESIGN.md`) o memorias semánticas (`.serena/memories/`), los indexa individualmente como fuentes de documentación sin forzar carpetas artificiales.

### Archivo de Configuración Local (`.engram.json`)
Al inicializar, se genera en la raíz del repositorio un archivo de configuración determinístico:

```json
{
  "project_id": "birdple-v2",
  "name": "birdple-v2",
  "docs": {
    "root": ".",
    "todo_file": "TODO.md",
    "sources": [
      "README.md",
      "SERVICES.md",
      ".serena/memories"
    ]
  },
  "created_at": "2026-11-24T02:30:00Z"
}
```

---

## 5. Gestión de Proyectos desde CLI (`engram project`)

El comando `engram project` ofrece control total sobre el ciclo de vida de los namespaces:

```bash
# Listar todos los proyectos activos registrados en el demonio
engram project ls

# Obtener detalles y configuración JSONB de un proyecto
engram project get birdple-v2

# Crear o actualizar un proyecto manualmente
engram project create my-service --name "My Microservice" --desc "Core Payment Gateway"

# Soft-delete seguro de un proyecto (marca deleted_at sin perder histórico)
engram project rm my-service --confirm my-service

# Purga física irreversible (elimina memoria, revisiones, tareas y eventos en cascada)
engram project rm my-service --confirm my-service --purge
```

---

## 6. Referencia de Comandos CLI

El CLI `engram` interactúa directamente con el demonio local o remoto:

```bash
# Variables de entorno opcionales
export ENGRAM_SERVER_URL="http://localhost:8085"
export ENGRAM_AUTH_TOKEN="engram_dev_token_2026"

# 1. Memory Plane (Descubrimiento Progresivo)
# Listar catálogo esqueletal sin volcar contenido (~1k tokens)
engram manifest my-project

# Hidratar contenido completo de un contrato específico bajo demanda
engram recall my-project "architecture/contracts"

# Búsqueda semántica híbrida (pgvector + pg_trgm)
engram search my-project "autenticación outbox nats" --limit 5

# Guardar o actualizar memoria con blame criptográfico y OCC
engram store my-project "architecture/contracts" ./docs/contracts.md \
  --rationale "Añadir soporte para webhooks" \
  --digest "Contratos HTTP y eventos de webhooks" \
  --occ --model "claude-opus-5-5"

# Auditar historial de revisiones, autores, modelos y cadena de hashes
engram blame my-project "architecture/contracts"

# 2. State Plane (Tareas & Handoffs)
# Crear y listar tareas operativas
engram task create my-project task_abc123 "Implementar webhooks" --priority 10
engram task ls my-project --status pending

# Reclamar siguiente tarea disponible con bloqueo atómico de fila (SKIP LOCKED)
engram task claim my-project

# Registrar un handoff inmutable y completar la tarea atómicamente en un solo paso
engram handoff my-project task_abc123 PASS "Migración completada exitosamente" "go test -v ./... -> 21 passed" --complete --lease <lease_id>
```

---

## 7. Prompts de Instrucción para Agentes de IA

Copia y pega los siguientes prompts al interactuar con tu asistente de codificación (Claude Code, Antigravity, Cursor, Gemini CLI) para operar en modo Dual-Plane:

### Prompt 1: Inicialización de Proyecto
```markdown
Configura este repositorio en el plano de memoria de Engram:
1. Comprueba si el demonio de Engram está activo en http://localhost:8085/healthz. Si está inactivo, levántalo con `docker compose up -d`.
2. Ejecuta `engram init` para detectar la topología de documentación y registrar el namespace del proyecto.
3. Verifica la configuración generada en `.engram.json` y confirma que el proyecto aparece listado en `engram project ls`.
```

### Prompt 2: Ejecución de Tarea en Modo Dual-Plane
```markdown
Actúa bajo las Directivas Dual-Plane de Engram:
1. NO realices volcados masivos de archivos al contexto (`read_file` sobre directorios completos). Consulta `list_memory_manifest` o `engram manifest <project>` e hidrata contratos específicos con `recall_memory`.
2. Mantén el árbol de Git limpio durante el desarrollo. No edites `TODO.md` mientras implementas código.
3. Reclama la tarea activa utilizando `claim_task` (SKIP LOCKED).
4. Implementa el código, resuelve la causa raíz y ejecuta pruebas en runtime para validar los efectos secundarios reales.
5. Al concluir, sincroniza atómicamente la documentación (`TODO.md`, `docs/`) en el mismo commit que introduce el código probado.
6. Registra un handoff inmutable con `record_handoff` indicando veredicto (PASS/FAIL), resumen de menos de 150 palabras y comandos de prueba reproducibles.
```

### Prompt 3: Handoff y Cierre de Sesión
```markdown
Registra el handoff de cierre para la tarea activa en Engram:
- Veredicto: PASS
- Resumen ejecutivo (<150 palabras) indicando componentes creados y contratos alterados.
- Evidencia reproducible: comando de prueba ejecutado y conteo exacto de asserts.
- Lista de artefactos modificados.
```

---

## 8. Verificación y Suite de Tests

Para verificar localmente la integridad del código, el linter y los contratos del sistema:

```bash
# Ejecutar suite de pruebas con detector de carreras y base de datos de prueba
go test -v -race -count=1 ./...

# Verificar calidad y análisis estático con golangci-lint
golangci-lint run ./...

# Compilar binarios de producción
go build -trimpath -ldflags="-s -w" ./cmd/engram
go build -trimpath -ldflags="-s -w" ./cmd/engramd
```

---

## 9. Navegación de Subsistemas
- [`cmd`](./cmd/README.md): Puntos de entrada ejecutables para el demonio (`engramd`) y la CLI de desarrollo (`engram`).
- [`internal`](./internal/README.md): Lógica de dominio, repositorios `sqlc`, protocolo MCP HTTP/SSE y outbox transaccional.
- [`migrations`](./migrations/README.md): Migraciones SQL canónicas para PostgreSQL 18 con `pgvector` y `pg_trgm`.
- [`plugins/engram`](./plugins/engram): Paquete del plugin oficial para Google Antigravity con reglas, hooks y runbook skill.
