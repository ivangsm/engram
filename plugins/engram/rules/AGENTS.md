# Engram Dual-Plane Directives

Reglas permanentes de operación para agentes de IA con el plano dual de memoria y estado (Git Canon + Engram Runtime).

0. Aplicabilidad
   - Estas directivas aplican únicamente en proyectos donde Engram esté inicializado/configurado y en tareas que involucren cambios de código, estado o arquitectura.
   - En consultas puras de información, sesiones sin workspace activo o repositorios sin Engram, omitir el flujo de `list_memory_manifest`, `claim_task` y `record_handoff`.

1. Zero Context Dump (Descubrimiento Progresivo)
   - Prohibido el volcado masivo de archivos de documentación al contexto (`read_file` sobre directorios completos).
   - Siempre invocar `list_memory_manifest` (o `engram manifest`) para inspeccionar el catálogo esqueletal (~1k tokens) antes de hidratar contratos específicos con `recall_memory`.

2. Inviolabilidad del Árbol Git en Vuelo (Aislamiento de Tareas)
   - El working tree de Git debe mantenerse limpio de tracking operativo durante la codificación activa.
   - Las tareas activas, estados en progreso y leases concurrentes viven exclusivamente en Engram (`claim_task` con `SKIP LOCKED`), nunca editando `TODO.md` mientras se implementa código.

3. Consolidación Tardía (Late Sync)
   - Los checkmarks de `TODO.md` (`- [x]`) y las actualizaciones a la documentación canónica (`docs/`, `README.md`) se sincronizan al finalizar la tarea, dejándolos preparados en el working tree para entrar atómicamente en el mismo commit que introduce el código probado cuando el usuario ordene el commit.
   - Invariantes arquitectónicos mutados se persisten en Engram mediante `store_memory` con `expected_parent_hash` (OCC).

4. Handoff Inmutable
   - Toda entrega o transición de tarea exige el registro de un acuse verificable mediante `record_handoff` (o `engram handoff`).
   - El handoff debe contener: veredicto (`PASS`, `FAIL`, `BLOCKED`, `NEEDS_REVIEW`), resumen ejecutivo (<150 palabras), evidencia reproducible (comandos de prueba, conteo de asserts) y lista serializada de artefactos modificados.
