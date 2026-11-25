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
	"strconv"
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
	ProjectID string
	RootDir   string
	Memories  []syncMemoryItem
	Tasks     []syncTaskItem
}

type syncOptions struct {
	ProjectID        string
	Path             string
	DocsDir          string
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
	reExplicitTask = regexp.MustCompile(`\b((?:TASK|PND|BUG|FEAT|FIX|ADR)-\d+)\b`)
	reChecklist    = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s+(.+)$`)
)

func runSync(serverURL, token string, args []string, out, errOut io.Writer) int {
	const usage = `Usage: engram sync [project_id] [options]

Auto-discovers and synchronizes documentation, Serena memories, EGM directory READMEs,
rules, and task files (pendientes/PND-*.md, TODO.md) into the Engram state plane.

Options:
  --project <id>         Project ID override (default: auto-detected from .engram.json / git / dir)
  --path <dir>           Target repository or workspace root (default: .)
  --docs <dir>           Additional or override docs directory (e.g. ../checamos-docs)
  --scope <scope>        Override scope for all synced memories
  --prefix <prefix>      Optional key prefix (e.g. service name)
  --dry-run              Discover and print plan without mutating Engram
  --force                Re-store memories even if content hash is unchanged
  --no-tasks             Skip task discovery (TODO.md, pendientes/PND-*.md)
  --include-completed    Include completed tasks from TODO.md / pendientes
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

	plan, err := buildSyncPlan(opts)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "Error building sync plan: %v\n", err)
		return 1
	}

	if opts.DryRun {
		printSyncDryRun(plan, out)
		return 0
	}

	if len(plan.Memories) == 0 && len(plan.Tasks) == 0 {
		_, _ = fmt.Fprintf(out, "Nothing to sync for project '%s' in %s\n", plan.ProjectID, plan.RootDir)
		return 0
	}

	return executeSyncPlan(serverURL, token, plan, opts, out, errOut)
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

	// Resolve explicit or configured docs directories
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

	// 2. Walk the target workspace for Serena, Rules, Docs, EGM READMEs, Root MDs, and Tasks
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

		// A. Tasks: pendientes/PND-*.md
		if strings.Contains(lowerRel, "pendientes/") && strings.HasPrefix(strings.ToUpper(name), "PND-") {
			if t, ok := parsePendienteFile(absPath, opts.IncludeCompleted); ok {
				addTask(t)
			}
			return nil
		}

		// B. Tasks: TODO.md anywhere in project
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

		// C. Serena memories (.serena/memories/... or .serena/...)
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

		// D. Rules (.agents/rules, .cursor/rules, AGENTS.md, CLAUDE.md, GEMINI.md)
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

		// E. Skip other hidden dot-directories (except .egm/)
		if strings.HasPrefix(relSlash, ".") && !strings.HasPrefix(lowerRel, ".egm/") {
			return nil
		}

		// F. Docs directories (docs/, doc/, documentation/, *-docs/)
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

		// G. EGM directory READMEs & .egm/
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

		// H. Root-level markdowns (ARCHITECTURE.md, CONVENTIONS.md, etc.)
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

	// Check .engram.json docs.root if configured
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

	// Sibling independent docs repo (e.g. ../<projectID>-docs, like checamos-docs)
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
	// Parse YAML frontmatter if present
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

func parsePendienteFile(absPath string, includeCompleted bool) (syncTaskItem, bool) {
	raw, err := readTrimmedFile(absPath)
	if err != nil || raw == "" {
		return syncTaskItem{}, false
	}

	base := stripMDExt(filepath.Base(absPath))
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	title := base
	priority := 0
	completed := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "status:") || strings.HasPrefix(lower, "estado:") {
			val := strings.ToLower(strings.TrimSpace(strings.SplitN(trimmed, ":", 2)[1]))
			if strings.Contains(val, "completed") || strings.Contains(val, "done") ||
				strings.Contains(val, "closed") || strings.Contains(val, "completad") ||
				strings.Contains(val, "resuelt") {
				completed = true
			}
		}
		if strings.HasPrefix(lower, "priority:") || strings.HasPrefix(lower, "prioridad:") {
			val := strings.ToLower(strings.TrimSpace(strings.SplitN(trimmed, ":", 2)[1]))
			if n, convErr := strconv.Atoi(val); convErr == nil {
				priority = n
			} else if strings.Contains(val, "high") || strings.Contains(val, "alta") || strings.Contains(val, "crit") {
				priority = 10
			} else if strings.Contains(val, "med") {
				priority = 5
			} else if strings.Contains(val, "low") || strings.Contains(val, "baja") {
				priority = 1
			}
		}
		if title == base && strings.HasPrefix(trimmed, "#") {
			h := cleanMarkdownInline(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			if h != "" {
				title = h
			}
		}
	}

	if completed && !includeCompleted {
		return syncTaskItem{}, false
	}

	return syncTaskItem{
		ID:          base,
		Title:       title,
		Description: raw,
		Priority:    priority,
		SourcePath:  absPath,
	}, true
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
	// 1. Ensure project exists
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

	// 2. Fetch existing manifest to avoid unnecessary blame/recall checks for new keys
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

	// 3. Sync memories concurrently
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
						rationale = "engram sync: " + m.SourcePath
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

	// 4. Sync tasks concurrently
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

	_, _ = fmt.Fprintf(out, "Engram sync complete for '%s':\n", plan.ProjectID)
	_, _ = fmt.Fprintf(out, "  Memories stored:    %d\n", storedCount.Load())
	_, _ = fmt.Fprintf(out, "  Memories unchanged: %d\n", skippedCount.Load())
	_, _ = fmt.Fprintf(out, "  Tasks synced:       %d\n", taskCount.Load())
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
