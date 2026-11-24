# Engram Antigravity Plugin

Plugin para Google Antigravity que integra el plano dual de memoria y estado de **Engram** (`engramd` MCP server, CLI y directivas de ejecución para agentes).

## Características

- **Descubrimiento Progresivo**: Manifiestos de memoria de ~1k tokens en lugar de volcados completos de archivos (`list_memory_manifest` + `recall_memory`).
- **Aislamiento de Tareas en Vuelo**: Cola atómica de tareas con leases excluyentes mediante PostgreSQL 18 `FOR UPDATE SKIP LOCKED` (`claim_task`, `renew_task_lease`).
- **Consolidación Tardía (Late Sync)**: Sincronización de documentación y checkmarks en Git atómicamente en el commit final que pasa verificación.
- **Handoffs Inmutables**: Registro criptográfico de veredictos (`PASS`, `FAIL`, etc.), resumen ejecutivo, evidencia terminal reproducible y artefactos modificados (`record_handoff`).
- **Blame Criptográfico**: Registro de autoría, modelo LLM, machine ID, hash SHA-256 encadenado y diffs unificados (`blame_memory`).
- **Lifecycle Hook `SessionStart`**: Detección automática del proyecto y verificación de conectividad al iniciar cada sesión.
- **Degradación Local Elegante**: Modo local sobre archivos Markdown si el demonio o contenedor no están disponibles.

## Estructura del Plugin

```text
plugins/engram/
├── plugin.json                 # Metadatos del plugin
├── mcp_config.json             # Configuración del servidor MCP (HTTP JSON-RPC / SSE)
├── hooks.json                  # Hook de ciclo de vida SessionStart
├── rules/
│   └── AGENTS.md               # Reglas permanentes (always-on) del plano dual
├── skills/
│   └── engram/
│       └── SKILL.md            # Skill canónica con protocolo de 4 fases y referencia de herramientas
├── scripts/
│   └── session_start.sh        # Script ejecutable de inicio de sesión
└── README.md                   # Documentación del plugin
```

## Configuración y Variables de Entorno

| Variable | Valor por Defecto | Descripción |
| :--- | :--- | :--- |
| `ENGRAM_SERVER_URL` | `http://127.0.0.1:8085` | URL base del demonio Engram |
| `ENGRAM_AUTH_TOKEN` | `engram_dev_token_2026` | Token Bearer para autenticación MCP / HTTP |
| `ENGRAM_PROJECT_ID` | Auto-detectado (git remote / git root) | Identificador de aislamiento de memoria y tareas |
| `ENGRAM_DOCS_DIR` | Auto-detectado (`docs/`, `./`) | Directorio canónico de documentación en Git |

## Herramientas MCP Expuestas

### Memoria
- `list_memory_manifest(project_id, scope?)`: Manifiesto ligero de invariantes y contratos.
- `recall_memory(project_id, key)`: Obtención de contenido completo de un invariante.
- `search_memory(project_id, query, limit?)`: Búsqueda híbrida (vectorial 0.65 + texto 0.35).
- `store_memory(project_id, key, content, rationale, ...)`: Persistencia con OCC (`expected_parent_hash`).
- `blame_memory(project_id, key)`: Trazabilidad criptográfica y auditoría de cambios.

### Estado y Tareas
- `create_task(project_id, task_id, title, description, priority, metadata?)`: Encolado de tareas operativas.
- `claim_task(project_id, task_id?, leased_by?, ttl_seconds?)`: Reclamo atómico con lease UUID.
- `renew_task_lease(project_id, task_id, lease_id, extend_seconds)`: Renovación cercada de lease.
- `record_handoff(project_id, task_id, from_agent, to_agent?, verdict, summary, reproducible_evidence, artifacts?)`: Entrega inmutable de tarea.
- `complete_task(project_id, task_id, lease_id?)`: Marcado de tarea completada y liberación de locks.
- `fail_task(project_id, task_id, lease_id?, reason)`: Registro de fallo con autopsia.

## Instalación Global en Antigravity

Para enlazar el plugin en Antigravity globalmente:

```bash
mkdir -p ~/.gemini/config/plugins
ln -sfn <path-to-engram>/plugins/engram ~/.gemini/config/plugins/engram
```
