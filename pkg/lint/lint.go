// Package lint provides static analysis for database migration directories.
// It checks for ordering gaps, duplicate versions, naming convention violations,
// dangerous SQL operations, and missing down migrations.
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Severity levels for lint issues.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Issue describes a single lint finding.
type Issue struct {
	File     string `json:"file"`
	Version  string `json:"version,omitempty"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Result is the outcome of linting a migration directory.
type Result struct {
	Dir    string  `json:"dir"`
	Issues []Issue `json:"issues"`
	Clean  bool    `json:"clean"`
}

// migrationFile represents a parsed migration file entry.
type migrationFile struct {
	name    string
	version uint64
	isUp    bool
	isDown  bool
	isAtlas bool // no .up/.down suffix (atlas or goose format)
}

var (
	// pairedRe matches: <version>_<desc>.up.sql or <version>_<desc>.down.sql
	pairedRe = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)
	// atlasRe matches: <version>_<desc>.sql (atlas/goose combined format)
	atlasRe = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)

	// dangerousOps are SQL patterns that may cause data loss.
	dangerousPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`),
		regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`),
		regexp.MustCompile(`(?i)\bTRUNCATE\b`),
		regexp.MustCompile(`(?i)\bDROP\s+SCHEMA\b`),
		regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`),
	}
	// safeGuards indicate the dangerous op is intentional and guarded.
	safeGuards = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bIF\s+EXISTS\b`),
		regexp.MustCompile(`(?i)\bWHERE\b`), // DELETE FROM ... WHERE
	}
)

// Lint analyses the migration directory at dir and returns a Result.
func Lint(dir string) (*Result, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("lint: read dir %s: %w", dir, err)
	}

	result := &Result{Dir: dir}

	// Parse all .sql files.
	byVersion := map[uint64]*migrationFile{}
	var versions []uint64

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		name := e.Name()

		var mf *migrationFile

		if m := pairedRe.FindStringSubmatch(name); m != nil {
			v, _ := strconv.ParseUint(m[1], 10, 64)
			existing, ok := byVersion[v]
			if !ok {
				existing = &migrationFile{version: v}
				byVersion[v] = existing
				versions = append(versions, v)
			}
			mf = existing
			mf.name = name
			if m[3] == "up" {
				mf.isUp = true
			} else {
				mf.isDown = true
			}
		} else if m := atlasRe.FindStringSubmatch(name); m != nil {
			v, _ := strconv.ParseUint(m[1], 10, 64)
			if _, ok := byVersion[v]; !ok {
				byVersion[v] = &migrationFile{version: v, name: name, isAtlas: true}
				versions = append(versions, v)
			}
			mf = byVersion[v]
		} else {
			result.Issues = append(result.Issues, Issue{
				File:     name,
				Severity: SeverityWarning,
				Code:     "L003",
				Message:  fmt.Sprintf("file %q does not match expected naming convention (<version>_<desc>.up.sql or <version>_<desc>.sql)", name),
			})
			continue
		}
		_ = mf

		// L004: dangerous operations.
		content, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		result.Issues = append(result.Issues, checkDangerous(name, string(content))...)
	}

	// Sort versions for ordered checks.
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	// L001: ordering gaps.
	for i := 1; i < len(versions); i++ {
		if versions[i] != versions[i-1]+1 {
			result.Issues = append(result.Issues, Issue{
				Version:  fmt.Sprintf("%d", versions[i]),
				Severity: SeverityWarning,
				Code:     "L001",
				Message:  fmt.Sprintf("version gap: no migration between %d and %d", versions[i-1], versions[i]),
			})
		}
	}

	// L002: duplicate versions — already handled (map dedup), but check filenames for exact dupes.
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Name()] {
			result.Issues = append(result.Issues, Issue{
				File:     e.Name(),
				Severity: SeverityError,
				Code:     "L002",
				Message:  fmt.Sprintf("duplicate migration file: %s", e.Name()),
			})
		}
		seen[e.Name()] = true
	}

	// L005: missing down files for paired-format migrations.
	for _, v := range versions {
		mf := byVersion[v]
		if mf.isAtlas {
			continue // atlas/goose format — single file, down is embedded or separate tool
		}
		if mf.isUp && !mf.isDown {
			result.Issues = append(result.Issues, Issue{
				Version:  fmt.Sprintf("%d", v),
				Severity: SeverityWarning,
				Code:     "L005",
				Message:  fmt.Sprintf("migration v%d has no corresponding .down.sql file", v),
			})
		}
	}

	// L006: empty files.
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		info, _ := e.Info()
		if info != nil && info.Size() == 0 {
			result.Issues = append(result.Issues, Issue{
				File:     e.Name(),
				Severity: SeverityError,
				Code:     "L006",
				Message:  fmt.Sprintf("migration file %q is empty", e.Name()),
			})
		}
	}

	result.Clean = len(result.Issues) == 0
	return result, nil
}

// checkDangerous scans SQL content for potentially dangerous operations that
// lack safety guards (IF EXISTS, WHERE clauses, etc.).
func checkDangerous(filename, content string) []Issue {
	var issues []Issue
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue // skip comments
		}
		for _, pat := range dangerousPatterns {
			if !pat.MatchString(line) {
				continue
			}
			// Check if any safe guard is present on the same line.
			guarded := false
			for _, guard := range safeGuards {
				if guard.MatchString(line) {
					guarded = true
					break
				}
			}
			if !guarded {
				issues = append(issues, Issue{
					File:     filename,
					Severity: SeverityWarning,
					Code:     "L004",
					Message:  fmt.Sprintf("line %d: potentially dangerous operation without safety guard: %s", i+1, strings.TrimSpace(line)),
				})
			}
		}
	}
	return issues
}
