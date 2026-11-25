package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ivan/engram/internal/mcp"
)

type syncMemoryItem struct {
	Key         string
	Scope       string
	Digest      string
	Content     string
	ContentHash string
	SourcePath  string
	Rationale   string
	Category    string
}

type syncTaskItem struct {
	ID          string
	Title       string
	Description string
	Priority    int
	SourcePath  string
}

type syncPlan struct {
	ProjectID        string
	RootDir          string
	Memories         []syncMemoryItem
	Tasks            []syncTaskItem
	CompletedTaskIDs []string
}

// curatedPlanFile defines the JSON schema accepted by `engram sync --plan <file.json | ->`.
// Agents use this after semantic curation to execute idempotent batch syncs.
type curatedPlanFile struct {
	ProjectID     string            `json:"project_id,omitempty"`
	Memories      []curatedMemory   `json:"memories,omitempty"`
	Tasks         []curatedTask     `json:"tasks,omitempty"`
	CompleteTasks []string          `json:"complete_tasks,omitempty"`
}

type curatedMemory struct {
	Key       string `json:"key"`
	Scope     string `json:"scope,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Content   string `json:"content,omitempty"`
	File      string `json:"file,omitempty"`
	Rationale string `json:"rationale,omitempty"`
}

type curatedTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Priority    int    `json:"priority,omitempty"`
}

type syncOptions struct {
	ProjectID        string
	Path             string
	DocsDir          string
	PlanFile         string
	ScopeOverride    string
	Prefix           string
	DryRun           bool
	Force            bool
	NoTasks          bool
	IncludeCompleted bool
	Only             string
	Concurrency      int
	Rationale        string
	Author           string
	Model            string
	MachineID        string
}

var ignoredSyncDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".next":        true,
	".nuxt":        true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".turbo":       true,
	".cache":       true,
	"bin":          true,
	"coverage":     true,
	".worktrees":   true,
	".orca":        true,
	".archify":     true,
	".idea":        true,
	".vscode":      true,
}

var (
	reMDLink       = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reExplicitTask = regexp.MustCompile(`\b((?:TASK|BUG|FEAT|FIX|ADR)-\d+)\b`)
	reChecklist    = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s+(.+)$`)
)

func runSync(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = `Usage: engram sync [project_id] [options]

Auto-discovers and synchronizes documentation, Serena memories, EGM directory READMEs,
rules, and TODO.md checklists into the Engram state plane, or executes a curated --plan JSON.

Options:
  --project <id>         Project ID override (default: auto-detected from .engram.json / git / dir)
  --path <dir>           Target repository or workspace root (default: .)
  --docs <dir>           Additional or override docs directory (e.g. ../checamos-docs)
  --plan <file|->        Execute a curated JSON plan file (or '-' for stdin) instead of auto-discovery
  --scope <scope>        Override scope for all synced memories
  --prefix <prefix>      Optional key prefix (e.g. service name)
  --dry-run              Discover/load plan and print summary without mutating Engram
  --force                Re-store memories even if content hash is unchanged
  --no-tasks             Skip TODO.md task discovery
  --include-completed    Include completed [x] items from TODO.md
  --only <sources>       Comma-separated filter: docs,serena,egm,rules,tasks
  --concurrency <n>      Number of parallel workers (default: 8)
  --rationale <text>     Custom rationale for memory revisions
  --author <user>        Author identifier (default: $ENGRAM_AUTHOR or $USER)
  --model <model_id>     Agent model identifier (default: $ENGRAM_AGENT_MODEL)
  --machine-id <host>    Machine hostname override (default: $ENGRAM_MACHINE_ID or hostname)
`
	fsFlags := flag.NewFlagSet("sync", flag.ContinueOnError)
	projectFlag := fsFlags.String("project", "", "Project ID override")
	pathFlag := fsFlags.String("path", ".", "Target repository or workspace root")
	docsFlag := fsFlags.String("docs", "", "Additional or override docs directory")
	planFlag := fsFlags.String("plan", "", "Curated JSON plan file or '-' for stdin")
	scopeFlag := fsFlags.String("scope", "", "Override scope for all synced memories")
	prefixFlag := fsFlags.String("prefix", "", "Optional key prefix")
	dryRunFlag := fsFlags.Bool("dry-run", false, "Discover without mutating Engram")
	forceFlag := fsFlags.Bool("force", false, "Re-store even if hash unchanged")
	noTasksFlag := fsFlags.Bool("no-tasks", false, "Skip task discovery")
	includeCompletedFlag := fsFlags.Bool("include-completed", false, "Include completed tasks")
	onlyFlag := fsFlags.String("only", "", "Comma-separated filter: docs,serena,egm,rules,tasks")
	concurrencyFlag := fsFlags.Int("concurrency", 8, "Number of parallel workers")
	rationaleFlag := fsFlags.String("rationale", "", "Custom rationale")
	authorFlag := fsFlags.String("author", "", "Author identifier")
	modelFlag := fsFlags.String("model", "", "Agent model identifier")
	machineFlag := fsFlags.String("machine-id", "", "Machine identifier")

	if help, ok := parseSubcommandFlags(fsFlags, args, usage, out, errOut); !ok {
		return 1
	} else if help {
		return 0
	}

	rootDir := strings.TrimSpace(*pathFlag)
	if rootDir == "" {
		rootDir = "."
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error resolving path %s: %v\n", rootDir, err)
		return 1
	}
	if !dirExists(absRoot) {
		_, _ = fmt.Fprintf(errOut, "Error: target path '%s' is not a directory\n", absRoot)
		return 1
	}

	explicitProject := strings.TrimSpace(*projectFlag)
	if explicitProject == "" && fsFlags.NArg() >= 1 {
		explicitProject = strings.TrimSpace(fsFlags.Arg(0))
	}
	projectID := detectProjectID(explicitProject, absRoot)

	conc := *concurrencyFlag
	if conc < 1 {
		conc = 1
	}
	if conc > 32 {
		conc = 32
	}

	opts := syncOptions{
		ProjectID:        projectID,
		Path:             absRoot,
		DocsDir:          strings.TrimSpace(*docsFlag),
		PlanFile:         strings.TrimSpace(*planFlag),
		ScopeOverride:    strings.TrimSpace(*scopeFlag),
		Prefix:           strings.Trim(strings.TrimSpace(*prefixFlag), "/"),
		DryRun:           *dryRunFlag,
		Force:            *forceFlag,
		NoTasks:          *noTasksFlag,
		IncludeCompleted: *includeCompletedFlag,
		Only:             strings.TrimSpace(*onlyFlag),
		Concurrency:      conc,
		Rationale:        strings.TrimSpace(*rationaleFlag),
		Author:           resolveAuthor(*authorFlag),
		Model:            resolveModel(*modelFlag),
		MachineID:        resolveMachineID(*machineFlag),
	}

	var plan *syncPlan
	if opts.PlanFile != "" {
		plan, err = loadCuratedPlan(opts)
	} else {
		plan, err = buildSyncPlan(opts)
	}
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error building sync plan: %v\n", err)
		return 1
	}

	if opts.DryRun {
		printSyncDryRun(plan, out)
		return 0
	}

	if len(plan.Memories) == 0 && len(plan.Tasks) == 0 && len(plan.CompletedTaskIDs) == 0 {
		_, _ = fmt.Fprintf(out, "Nothing to sync for project '%s' in %s\n", plan.ProjectID, plan.RootDir)
		return 0
	}

	return executeSyncPlan(serverURL, token, plan, opts, out, errOut)
}

func loadCuratedPlan(opts syncOptions) (*syncPlan, error) {
	var data []byte
	var err error
	if opts.PlanFile == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(opts.PlanFile)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read plan %s: %w", opts.PlanFile, err)
	}

	var raw curatedPlanFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid plan JSON: %w", err)
	}

	projectID := opts.ProjectID
	if strings.TrimSpace(raw.ProjectID) != "" && projectID == detectProjectID("", opts.Path) {
		projectID = strings.TrimSpace(raw.ProjectID)
	}

	plan := &syncPlan{
		ProjectID: projectID,
		RootDir:   opts.Path,
	}

	seenKeys := make(map[string]bool)
	for _, m := range raw.Memories {
		key := strings.TrimSpace(m.Key)
		if key == "" {
			continue
		}
		if opts.Prefix != "" {
			key = opts.Prefix + "/" + key
		}
		key = sanitizeMemoryKey(key)
		if seenKeys[key] {
			continue
		}

		content := strings.TrimRight(m.Content, "\r\n")
		sourcePath := m.File
		if content == "" && m.File != "" {
			fp := m.File
			if !filepath.IsAbs(fp) {
				fp = filepath.Join(opts.Path, fp)
			}
			content, err = readTrimmedFile(fp)
			if err != nil {
				return nil, fmt.Errorf("memory %q file %s: %w", key, m.File, err)
			}
		}
		if strings.TrimSpace(content) == "" {
			continue
		}

		scope := strings.TrimSpace(m.Scope)
		if opts.ScopeOverride != "" {
			scope = opts.ScopeOverride
		}
		if scope == "" {
			scope = "global"
		}
		scope = clampString(scope, 64)

		digest := strings.TrimSpace(m.Digest)
		if digest == "" {
			digest = extractDigest(content, filepath.Base(key))
		} else {
			digest = clampString(digest, 120)
		}

		seenKeys[key] = true
		plan.Memories = append(plan.Memories, syncMemoryItem{
			Key:         key,
			Scope:       scope,
			Digest:      digest,
			Content:     content,
			ContentHash: sha256Hex(content),
			SourcePath:  sourcePath,
			Rationale:   strings.TrimSpace(m.Rationale),
		})
	}

	if !opts.NoTasks {
		seenTasks := make(map[string]bool)
		for _, t := range raw.Tasks {
			id := strings.TrimSpace(t.ID)
			title := strings.TrimSpace(t.Title)
			if id == "" || title == "" {
				continue
			}
			if opts.Prefix != "" && !strings.HasPrefix(id, opts.Prefix+"-") {
				id = opts.Prefix + "-" + id
			}
			id = clampString(id, 128)
			if seenTasks[id] {
				continue
			}
			seenTasks[id] = true
			plan.Tasks = append(plan.Tasks, syncTaskItem{
				ID:          id,
				Title:       clampString(title, 255),
				Description: t.Description,
				Priority:    t.Priority,
			})
		}
		for _, id := range raw.CompleteTasks {
			tid := strings.TrimSpace(id)
			if tid == "" {
				continue
			}
			if opts.Prefix != "" && !strings.HasPrefix(tid, opts.Prefix+"-") {
				tid = opts.Prefix + "-" + tid
			}
			plan.CompletedTaskIDs = append(plan.CompletedTaskIDs, clampString(tid, 128))
		}
	}

	return plan, nil
}

func parseOnlyFilter(only string) map[string]bool {
	if strings.TrimSpace(only) == "" {
		return nil
	}
	allowed := make(map[string]bool)
	for _, part := range strings.Split(only, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p != "" {
			allowed[p] = true
		}
	}
	return allowed
}

func categoryAllowed(allowed map[string]bool, category string) bool {
	if len(allowed) == 0 {
		return true
	}
	return allowed[category]
}

func buildSyncPlan(opts syncOptions) (*syncPlan, error) {
	allowed := parseOnlyFilter(opts.Only)
	plan := &syncPlan{
		ProjectID: opts.ProjectID,
		RootDir:   opts.Path,
	}

	explicitDocsDirs := resolveExplicitDocsDirs(opts.Path, opts.ProjectID, opts.DocsDir)
	explicitDocsSet := make(map[string]bool, len(explicitDocsDirs))
	for _, d := range explicitDocsDirs {
		explicitDocsSet[d] = true
	}

	seenFiles := make(map[string]bool)
	seenKeys := make(map[string]bool)
	seenTasks := make(map[string]bool)

	addMemory := func(item syncMemoryItem) {
		if strings.TrimSpace(item.Content) == "" {
			return
		}
		if opts.Prefix != "" {
			item.Key = opts.Prefix + "/" + item.Key
		}
		if opts.ScopeOverride != "" {
			item.Scope = opts.ScopeOverride
		}
		item.Key = sanitizeMemoryKey(item.Key)
		item.Scope = clampString(item.Scope, 64)
		if seenKeys[item.Key] {
			return
		}
		seenKeys[item.Key] = true
		plan.Memories = append(plan.Memories, item)
	}

	addTask := func(task syncTaskItem) {
		if opts.NoTasks || !categoryAllowed(allowed, "tasks") {
			return
		}
		if opts.Prefix != "" && !strings.HasPrefix(task.ID, opts.Prefix+"-") {
			task.ID = opts.Prefix + "-" + task.ID
		}
		task.ID = clampString(task.ID, 128)
		task.Title = clampString(task.Title, 255)
		if task.ID == "" || task.Title == "" || seenTasks[task.ID] {
			return
		}
		seenTasks[task.ID] = true
		plan.Tasks = append(plan.Tasks, task)
	}

	// 1. Process explicit/sibling docs directories first (e.g. --docs ../checamos-docs, .engram.json docs.root, <proj>-docs)
	for _, docsDir := range explicitDocsDirs {
		err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				if ignoredSyncDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !isMarkdownFile(d.Name()) {
				return nil
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				return nil
			}
			seenFiles[absPath] = true

			rel, err := filepath.Rel(docsDir, absPath)
			if err != nil {
				rel = filepath.Base(absPath)
			}
			relSlash := filepath.ToSlash(rel)

			if strings.EqualFold(filepath.Base(absPath), "TODO.md") {
				for _, t := range parseTodoFile(absPath, relSlash, opts.IncludeCompleted) {
					addTask(t)
				}
				return nil
			}

			if !categoryAllowed(allowed, "docs") {
				return nil
			}

			raw, err := readTrimmedFile(absPath)
			if err != nil || raw == "" {
				return nil
			}
			scope := "docs"
			lowerRel := strings.ToLower(relSlash)
			if strings.HasPrefix(lowerRel, "adr/") || strings.HasPrefix(lowerRel, "adrs/") || strings.HasPrefix(strings.ToLower(filepath.Base(absPath)), "adr-") {
				scope = "adr"
			}
			key := "docs/" + stripMDExt(relSlash)
			addMemory(syncMemoryItem{
				Key:         key,
				Scope:       scope,
				Digest:      extractDigest(raw, filepath.Base(absPath)),
				Content:     raw,
				ContentHash: sha256Hex(raw),
				SourcePath:  absPath,
				Category:    "docs",
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// 2. Walk the target workspace for Serena, Rules, Docs, EGM READMEs, Root MDs, and TODO.md
	err := filepath.WalkDir(opts.Path, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if absPath != opts.Path && ignoredSyncDirs[d.Name()] {
				return filepath.SkipDir
			}
			if explicitDocsSet[absPath] {
				return filepath.SkipDir
			}
			return nil
		}

		if seenFiles[absPath] {
			return nil
		}

		name := d.Name()
		lowerName := strings.ToLower(name)
		isMDC := strings.HasSuffix(lowerName, ".mdc")
		if !isMarkdownFile(name) && !isMDC {
			return nil
		}

		rel, err := filepath.Rel(opts.Path, absPath)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		lowerRel := strings.ToLower(relSlash)

		// A. Standard TODO.md checklists
		if strings.EqualFold(name, "TODO.md") {
			for _, t := range parseTodoFile(absPath, relSlash, opts.IncludeCompleted) {
				addTask(t)
			}
			return nil
		}

		raw, err := readTrimmedFile(absPath)
		if err != nil || raw == "" {
			return nil
		}

		// B. Serena memories (.serena/memories/... or .serena/...)
		if strings.HasPrefix(lowerRel, ".serena/") || strings.Contains(lowerRel, "/.serena/") {
			if !categoryAllowed(allowed, "serena") {
				return nil
			}
			keyPart := relSlash
			keyPart = strings.Replace(keyPart, ".serena/memories/", "", 1)
			keyPart = strings.Replace(keyPart, ".serena/", "", 1)
			addMemory(syncMemoryItem{
				Key:         "serena/" + stripMDExt(keyPart),
				Scope:       "serena",
				Digest:      extractDigest(raw, name),
				Content:     raw,
				ContentHash: sha256Hex(raw),
				SourcePath:  relSlash,
				Category:    "serena",
			})
			return nil
		}

		// C. Rules (.agents/rules, .cursor/rules, AGENTS.md, CLAUDE.md, GEMINI.md)
		if strings.HasPrefix(lowerRel, ".agents/rules/") ||
			strings.HasPrefix(lowerRel, ".cursor/rules/") ||
			strings.Contains(lowerRel, "/.agents/rules/") ||
			name == "AGENTS.md" || name == "CLAUDE.md" || name == "GEMINI.md" {
			if !categoryAllowed(allowed, "rules") {
				return nil
			}
			keyPart := relSlash
			keyPart = strings.Replace(keyPart, ".agents/rules/", "", 1)
			keyPart = strings.Replace(keyPart, ".cursor/rules/", "", 1)
			addMemory(syncMemoryItem{
				Key:         "rules/" + stripMDExt(keyPart),
				Scope:       "rules",
				Digest:      extractDigest(raw, name),
				Content:     raw,
				ContentHash: sha256Hex(raw),
				SourcePath:  relSlash,
				Category:    "rules",
			})
			return nil
		}

		// D. Skip other hidden dot-directories (except .egm/)
		if strings.HasPrefix(relSlash, ".") && !strings.HasPrefix(lowerRel, ".egm/") {
			return nil
		}

		// E. Docs directories (docs/, doc/, documentation/, *-docs/)
		if isInsideDocsDir(lowerRel) {
			if !categoryAllowed(allowed, "docs") {
				return nil
			}
			scope := "docs"
			if strings.Contains(lowerRel, "/adr/") || strings.Contains(lowerRel, "/adrs/") ||
				strings.HasPrefix(lowerRel, "adr/") || strings.HasPrefix(lowerName, "adr-") {
				scope = "adr"
			}
			keyPart := trimLeadingDocsPrefix(relSlash)
			addMemory(syncMemoryItem{
				Key:         "docs/" + stripMDExt(keyPart),
				Scope:       scope,
				Digest:      extractDigest(raw, name),
				Content:     raw,
				ContentHash: sha256Hex(raw),
				SourcePath:  relSlash,
				Category:    "docs",
			})
			return nil
		}

		// F. EGM directory READMEs & .egm/
		if strings.EqualFold(name, "README.md") {
			if !categoryAllowed(allowed, "egm") {
				return nil
			}
			if relSlash == "README.md" {
				addMemory(syncMemoryItem{
					Key:         "root/README",
					Scope:       "root",
					Digest:      extractDigest(raw, name),
					Content:     raw,
					ContentHash: sha256Hex(raw),
					SourcePath:  relSlash,
					Category:    "egm",
				})
				return nil
			}
			addMemory(syncMemoryItem{
				Key:         "egm/" + stripMDExt(relSlash),
				Scope:       "egm",
				Digest:      extractDigest(raw, name),
				Content:     raw,
				ContentHash: sha256Hex(raw),
				SourcePath:  relSlash,
				Category:    "egm",
			})
			return nil
		}

		if strings.HasPrefix(lowerRel, ".egm/") {
			if !categoryAllowed(allowed, "egm") {
				return nil
			}
			keyPart := strings.TrimPrefix(relSlash, ".egm/")
			addMemory(syncMemoryItem{
				Key:         "egm/" + stripMDExt(keyPart),
				Scope:       "egm",
				Digest:      extractDigest(raw, name),
				Content:     raw,
				ContentHash: sha256Hex(raw),
				SourcePath:  relSlash,
				Category:    "egm",
			})
			return nil
		}

		// G. Root-level markdowns (ARCHITECTURE.md, CONVENTIONS.md, etc.)
		if !strings.Contains(relSlash, "/") {
			if !categoryAllowed(allowed, "docs") {
				return nil
			}
			addMemory(syncMemoryItem{
				Key:         "root/" + stripMDExt(relSlash),
				Scope:       "root",
				Digest:      extractDigest(raw, name),
				Content:     raw,
				ContentHash: sha256Hex(raw),
				SourcePath:  relSlash,
				Category:    "docs",
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(plan.Memories, func(i, j int) bool {
		return plan.Memories[i].Key < plan.Memories[j].Key
	})
	sort.Slice(plan.Tasks, func(i, j int) bool {
		return plan.Tasks[i].ID < plan.Tasks[j].ID
	})

	return plan, nil
}

func resolveExplicitDocsDirs(absRoot, projectID, docsFlag string) []string {
	var candidates []string

	if docsFlag != "" {
		p := docsFlag
		if !filepath.IsAbs(p) {
			p = filepath.Join(absRoot, p)
		}
		if abs, err := filepath.Abs(p); err == nil && dirExists(abs) {
			candidates = append(candidates, abs)
		}
	}

	if cfgPath := findEngramConfig(absRoot); cfgPath != "" {
		if data, err := os.ReadFile(cfgPath); err == nil {
			var cfg EngramConfig
			if json.Unmarshal(data, &cfg) == nil && cfg.Docs.Root != "" && cfg.Docs.Root != "." {
				p := cfg.Docs.Root
				if !filepath.IsAbs(p) {
					p = filepath.Join(filepath.Dir(cfgPath), p)
				}
				if abs, err := filepath.Abs(p); err == nil && dirExists(abs) {
					candidates = append(candidates, abs)
				}
			}
		}
	}

	if projectID != "" {
		siblingDocs := filepath.Join(filepath.Dir(absRoot), projectID+"-docs")
		if dirExists(siblingDocs) {
			if abs, err := filepath.Abs(siblingDocs); err == nil {
				candidates = append(candidates, abs)
			}
		}
	}

	var unique []string
	seen := make(map[string]bool)
	for _, c := range candidates {
		clean := filepath.Clean(c)
		if !seen[clean] {
			seen[clean] = true
			unique = append(unique, clean)
		}
	}
	return unique
}

func isInsideDocsDir(lowerRel string) bool {
	parts := strings.Split(lowerRel, "/")
	if len(parts) < 2 {
		return false
	}
	for _, seg := range parts[:len(parts)-1] {
		if seg == "docs" || seg == "doc" || seg == "documentation" || strings.HasSuffix(seg, "-docs") {
			return true
		}
	}
	return false
}

func trimLeadingDocsPrefix(relSlash string) string {
	lower := strings.ToLower(relSlash)
	for _, prefix := range []string{"docs/", "doc/", "documentation/"} {
		if strings.HasPrefix(lower, prefix) {
			return relSlash[len(prefix):]
		}
	}
	parts := strings.SplitN(relSlash, "/", 2)
	if len(parts) == 2 && strings.HasSuffix(strings.ToLower(parts[0]), "-docs") {
		return parts[1]
	}
	return relSlash
}

func isMarkdownFile(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".md")
}

func stripMDExt(path string) string {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".md") {
		return path[:len(path)-3]
	}
	if strings.HasSuffix(lower, ".mdc") {
		return path[:len(path)-4]
	}
	return path
}

func readTrimmedFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sanitizeMemoryKey(key string) string {
	key = strings.ReplaceAll(key, " ", "-")
	key = strings.Trim(key, "/")
	if len(key) <= 255 {
		return key
	}
	h := sha256Hex(key)[:8]
	return key[:246] + "-" + h
}

func clampString(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func extractDigest(content, fallbackFilename string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	startIdx := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "---" {
				startIdx = i + 1
				break
			}
			lower := strings.ToLower(trimmed)
			for _, field := range []string{"digest:", "description:", "summary:"} {
				if strings.HasPrefix(lower, field) {
					val := strings.TrimSpace(trimmed[len(field):])
					val = strings.Trim(val, `"'`)
					if val != "" {
						return clampString(cleanMarkdownInline(val), 120)
					}
				}
			}
		}
	}

	var heading string
	var bodyLine string
	inCodeBlock := false

	for i := startIdx; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock || trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if heading == "" {
				heading = cleanMarkdownInline(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			}
			continue
		}
		if strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "---") {
			continue
		}
		bodyLine = cleanMarkdownInline(strings.TrimLeft(trimmed, ">-* "))
		if bodyLine != "" {
			break
		}
	}

	var combined string
	switch {
	case heading != "" && bodyLine != "" && !strings.EqualFold(heading, bodyLine):
		combined = heading + " — " + bodyLine
	case heading != "":
		combined = heading
	case bodyLine != "":
		combined = bodyLine
	default:
		combined = stripMDExt(fallbackFilename)
	}

	return clampString(combined, 120)
}

func cleanMarkdownInline(s string) string {
	s = reMDLink.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func parseTodoFile(absPath, relSlash string, includeCompleted bool) []syncTaskItem {
	raw, err := readTrimmedFile(absPath)
	if err != nil || raw == "" {
		return nil
	}

	var tasks []syncTaskItem
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	section := ""

	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			section = cleanMarkdownInline(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			continue
		}
		m := reChecklist.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		isDone := strings.EqualFold(m[1], "x")
		if isDone && !includeCompleted {
			continue
		}
		text := cleanMarkdownInline(m[2])
		if text == "" {
			continue
		}

		taskID := ""
		if idMatch := reExplicitTask.FindStringSubmatch(text); len(idMatch) == 2 {
			taskID = idMatch[1]
		} else {
			taskID = "TODO-" + strings.ToUpper(sha256Hex(relSlash+"|"+text)[:8])
		}

		desc := "Source: " + relSlash
		if section != "" {
			desc += " (Section: " + section + ")"
		}
		tasks = append(tasks, syncTaskItem{
			ID:          taskID,
			Title:       text,
			Description: desc,
			Priority:    0,
			SourcePath:  relSlash,
		})
	}
	return tasks
}

func printSyncDryRun(plan *syncPlan, out io.Writer) {
	_, _ = fmt.Fprintf(out, "Dry-run sync plan for project '%s' (%s):\n", plan.ProjectID, plan.RootDir)
	_, _ = fmt.Fprintf(out, "  Memories discovered: %d\n", len(plan.Memories))
	_, _ = fmt.Fprintf(out, "  Tasks discovered:    %d\n", len(plan.Tasks))
	if len(plan.CompletedTaskIDs) > 0 {
		_, _ = fmt.Fprintf(out, "  Tasks to complete:   %d\n", len(plan.CompletedTaskIDs))
	}

	byScope := make(map[string]int)
	for _, m := range plan.Memories {
		byScope[m.Scope]++
	}
	if len(byScope) > 0 {
		scopes := make([]string, 0, len(byScope))
		for k := range byScope {
			scopes = append(scopes, k)
		}
		sort.Strings(scopes)
		_, _ = fmt.Fprintln(out, "\nMemories by scope:")
		for _, s := range scopes {
			_, _ = fmt.Fprintf(out, "  - %-10s %d\n", s, byScope[s])
		}
		_, _ = fmt.Fprintln(out, "\nSample memories:")
		limit := len(plan.Memories)
		if limit > 25 {
			limit = 25
		}
		for _, m := range plan.Memories[:limit] {
			_, _ = fmt.Fprintf(out, "  [%s] %s — %s\n", m.Scope, m.Key, m.Digest)
		}
		if len(plan.Memories) > limit {
			_, _ = fmt.Fprintf(out, "  ... and %d more\n", len(plan.Memories)-limit)
		}
	}

	if len(plan.Tasks) > 0 {
		_, _ = fmt.Fprintln(out, "\nSample tasks:")
		limit := len(plan.Tasks)
		if limit > 15 {
			limit = 15
		}
		for _, t := range plan.Tasks[:limit] {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", t.ID, t.Title)
		}
		if len(plan.Tasks) > limit {
			_, _ = fmt.Fprintf(out, "  ... and %d more\n", len(plan.Tasks)-limit)
		}
	}
}

func executeSyncPlan(serverURL, token string, plan *syncPlan, opts syncOptions, out, errOut io.Writer) int {
	projRes, err := callTool(serverURL, token, "create_project", map[string]interface{}{
		"project_id":  plan.ProjectID,
		"name":        plan.ProjectID,
		"description": fmt.Sprintf("Project %s synchronized via engram sync", plan.ProjectID),
	})
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error ensuring project '%s': %v\n", plan.ProjectID, err)
		return 1
	}
	if projRes.IsError {
		_, _ = fmt.Fprintf(errOut, "Error ensuring project '%s': %s\n", plan.ProjectID, firstToolText(projRes))
		return 1
	}

	existingKeys := make(map[string]bool)
	if !opts.Force {
		manRes, manErr := callTool(serverURL, token, "list_memory_manifest", map[string]string{
			"project_id": plan.ProjectID,
		})
		if manErr == nil && !manRes.IsError && len(manRes.Content) > 0 {
			var items []struct {
				Key string `json:"key"`
			}
			if json.Unmarshal([]byte(manRes.Content[0].Text), &items) == nil {
				for _, it := range items {
					existingKeys[it.Key] = true
				}
			}
		}
	}

	var storedCount atomic.Int64
	var skippedCount atomic.Int64
	var taskCount atomic.Int64
	var completedCount atomic.Int64
	var errCount atomic.Int64

	var errMu sync.Mutex
	var firstErrors []string
	recordErr := func(msg string) {
		errCount.Add(1)
		errMu.Lock()
		if len(firstErrors) < 10 {
			firstErrors = append(firstErrors, msg)
		}
		errMu.Unlock()
	}

	if len(plan.Memories) > 0 {
		memCh := make(chan syncMemoryItem, len(plan.Memories))
		for _, m := range plan.Memories {
			memCh <- m
		}
		close(memCh)

		var wg sync.WaitGroup
		workers := opts.Concurrency
		if workers > len(plan.Memories) {
			workers = len(plan.Memories)
		}
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for m := range memCh {
					if !opts.Force && existingKeys[m.Key] {
						headHash, err := fetchCurrentHashForOCC(serverURL, token, plan.ProjectID, m.Key)
						if err == nil && headHash == m.ContentHash {
							skippedCount.Add(1)
							continue
						}
					}

					rationale := opts.Rationale
					if rationale == "" {
						rationale = m.Rationale
					}
					if rationale == "" {
						if m.SourcePath != "" {
							rationale = "engram sync: " + m.SourcePath
						} else {
							rationale = "engram sync: curated plan"
						}
					}

					payload := map[string]interface{}{
						"project_id":  plan.ProjectID,
						"key":         m.Key,
						"content":     m.Content,
						"digest":      m.Digest,
						"rationale":   rationale,
						"scope":       m.Scope,
						"author_user": opts.Author,
						"machine_id":  opts.MachineID,
					}
					if opts.Model != "" {
						payload["author_agent_model"] = opts.Model
					}

					res, err := callTool(serverURL, token, "store_memory", payload)
					if err != nil {
						recordErr(fmt.Sprintf("memory %s: %v", m.Key, err))
						continue
					}
					if res.IsError {
						recordErr(fmt.Sprintf("memory %s: %s", m.Key, firstToolText(res)))
						continue
					}
					storedCount.Add(1)
				}
			}()
		}
		wg.Wait()
	}

	if len(plan.Tasks) > 0 {
		taskCh := make(chan syncTaskItem, len(plan.Tasks))
		for _, t := range plan.Tasks {
			taskCh <- t
		}
		close(taskCh)

		var wg sync.WaitGroup
		workers := opts.Concurrency
		if workers > len(plan.Tasks) {
			workers = len(plan.Tasks)
		}
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for t := range taskCh {
					payload := map[string]interface{}{
						"project_id":  plan.ProjectID,
						"task_id":     t.ID,
						"title":       t.Title,
						"description": t.Description,
						"priority":    t.Priority,
					}
					res, err := callTool(serverURL, token, "create_task", payload)
					if err != nil {
						recordErr(fmt.Sprintf("task %s: %v", t.ID, err))
						continue
					}
					if res.IsError {
						recordErr(fmt.Sprintf("task %s: %s", t.ID, firstToolText(res)))
						continue
					}
					taskCount.Add(1)
				}
			}()
		}
		wg.Wait()
	}

	if len(plan.CompletedTaskIDs) > 0 {
		existingPending := make(map[string]bool)
		lsRes, lsErr := callTool(serverURL, token, "list_tasks", map[string]interface{}{
			"project_id": plan.ProjectID,
			"status":     "pending",
			"limit":      2000,
		})
		if lsErr == nil && !lsRes.IsError && len(lsRes.Content) > 0 {
			var pendingTasks []struct {
				ID string `json:"id"`
			}
			if json.Unmarshal([]byte(lsRes.Content[0].Text), &pendingTasks) == nil {
				for _, pt := range pendingTasks {
					existingPending[pt.ID] = true
				}
			}
		}

		var toComplete []string
		for _, id := range plan.CompletedTaskIDs {
			if existingPending[id] {
				toComplete = append(toComplete, id)
			}
		}

		if len(toComplete) > 0 {
			compCh := make(chan string, len(toComplete))
			for _, id := range toComplete {
				compCh <- id
			}
			close(compCh)

			var wg sync.WaitGroup
			workers := opts.Concurrency
			if workers > len(toComplete) {
				workers = len(toComplete)
			}
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for id := range compCh {
						claimRes, err := callTool(serverURL, token, "claim_task", map[string]interface{}{
							"project_id":  plan.ProjectID,
							"task_id":     id,
							"leased_by":   opts.Author,
							"ttl_seconds": 60,
						})
						if err != nil {
							recordErr(fmt.Sprintf("claim closed task %s: %v", id, err))
							continue
						}
						if claimRes.IsError {
							recordErr(fmt.Sprintf("claim closed task %s: %s", id, firstToolText(claimRes)))
							continue
						}
						var claimed struct {
							LeaseID string `json:"lease_id"`
						}
						if len(claimRes.Content) == 0 || json.Unmarshal([]byte(claimRes.Content[0].Text), &claimed) != nil || claimed.LeaseID == "" {
							recordErr(fmt.Sprintf("claim closed task %s: missing lease_id", id))
							continue
						}

						res, err := callTool(serverURL, token, "complete_task", map[string]interface{}{
							"project_id": plan.ProjectID,
							"task_id":    id,
							"lease_id":   claimed.LeaseID,
						})
						if err != nil {
							recordErr(fmt.Sprintf("complete task %s: %v", id, err))
							continue
						}
						if res.IsError {
							recordErr(fmt.Sprintf("complete task %s: %s", id, firstToolText(res)))
							continue
						}
						completedCount.Add(1)
					}
				}()
			}
			wg.Wait()
		}
	}

	_, _ = fmt.Fprintf(out, "Engram sync complete for '%s':\n", plan.ProjectID)
	_, _ = fmt.Fprintf(out, "  Memories stored:    %d\n", storedCount.Load())
	_, _ = fmt.Fprintf(out, "  Memories unchanged: %d\n", skippedCount.Load())
	_, _ = fmt.Fprintf(out, "  Tasks synced:       %d\n", taskCount.Load())
	if completedCount.Load() > 0 {
		_, _ = fmt.Fprintf(out, "  Tasks reconciled:   %d (marked completed)\n", completedCount.Load())
	}
	if errCount.Load() > 0 {
		_, _ = fmt.Fprintf(errOut, "  Errors:             %d\n", errCount.Load())
		for _, e := range firstErrors {
			_, _ = fmt.Fprintf(errOut, "    - %s\n", e)
		}
		return 1
	}
	return 0
}

func firstToolText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return "unknown error"
	}
	return res.Content[0].Text
}
