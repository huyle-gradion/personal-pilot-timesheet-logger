package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ObsidianSink exports finalized timesheet records into the Obsidian AI Database vault.
type ObsidianSink struct {
	vaultPath string
}

// NewObsidianSink initializes an ObsidianSink with path resolution from:
// 1. Explicit customVaultPath
// 2. TIMESHEET_VAULT_PATH environment variable
// 3. AI_DATABASE_PATH environment variable
func NewObsidianSink(customVaultPath string) (*ObsidianSink, error) {
	vault := strings.TrimSpace(customVaultPath)
	if vault == "" {
		vault = strings.TrimSpace(os.Getenv("TIMESHEET_VAULT_PATH"))
	}
	if vault == "" {
		vault = strings.TrimSpace(os.Getenv("AI_DATABASE_PATH"))
	}

	if vault == "" {
		return nil, ErrSinkNotConfigured
	}

	cleaned := filepath.Clean(vault)
	stat, err := os.Stat(cleaned)
	if err != nil || !stat.IsDir() {
		return nil, fmt.Errorf("configured obsidian vault path does not exist or is not a directory: %s", cleaned)
	}

	return &ObsidianSink{vaultPath: cleaned}, nil
}

// Name returns the identifier for this exporter sink.
func (s *ObsidianSink) Name() string {
	return "obsidian"
}

// VaultPath returns the configured root directory of the Obsidian vault.
func (s *ObsidianSink) VaultPath() string {
	return s.vaultPath
}

// dailyNoteFrontmatterTemplate is the ONE daily-note YAML frontmatter schema,
// shared by the Obsidian sink and the managed export (internal/export). Format
// verb: the plain, unquoted YYYY-MM-DD date. The journal collector reads the
// `date` key to certify a note's date, so every created note must carry it.
const dailyNoteFrontmatterTemplate = `---
type: log
date: %s
log_type: daily
tags:
  - daily-log
  - timesheet
---
`

// DailyNoteFrontmatter returns the daily-note frontmatter for date. It is the
// single source of the template: callers must never mirror it.
func DailyNoteFrontmatter(date string) string {
	return fmt.Sprintf(dailyNoteFrontmatterTemplate, date)
}

// FormatMarkdownTable converts a TimesheetRecord into a standardized markdown section.
func FormatMarkdownTable(record TimesheetRecord) string {
	var sb strings.Builder
	sb.WriteString("## Clustered timesheet\n")
	sb.WriteString(fmt.Sprintf("- **Total hours**: %.2fh\n", record.TotalHours))
	sb.WriteString(fmt.Sprintf("- **Last updated**: %s (v%d)\n\n", record.UpdatedAt.Format(time.RFC3339), record.Version))
	sb.WriteString("| Start | End | Source | Category | Title | Details |\n")
	sb.WriteString("|---|---|---|---|---|---|\n")

	for _, block := range record.Blocks {
		title := strings.ReplaceAll(block.Title, "|", "\\|")
		desc := strings.ReplaceAll(block.Description, "|", "\\|")
		desc = strings.ReplaceAll(desc, "\n", " ")
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
			block.StartTime,
			block.EndTime,
			block.Source,
			block.Category,
			title,
			desc,
		))
	}

	return sb.String()
}

// Export writes the formatted timesheet section into the corresponding daily note in 02 - Daily & Logs/.
func (s *ObsidianSink) Export(ctx context.Context, record TimesheetRecord) error {
	Assert(ctx != nil, AssertStorageContextNil)
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := ValidateDate(record.Date); err != nil {
		return err
	}

	dailyDir := filepath.Join(s.vaultPath, "02 - Daily & Logs")
	if err := os.MkdirAll(dailyDir, DirPermPrivate); err != nil {
		return fmt.Errorf("failed creating daily logs directory in vault: %w", err)
	}

	notePath := filepath.Join(dailyDir, record.Date+".md")
	renderedSection := FormatMarkdownTable(record)

	var fullContent string
	existingBytes, err := os.ReadFile(notePath)
	if err == nil {
		fullContent = replaceOrAppendTimesheetSection(string(existingBytes), renderedSection)
	} else if os.IsNotExist(err) {
		fullContent = fmt.Sprintf("%s\n# Daily work log: %s\n\n%s",
			DailyNoteFrontmatter(record.Date), record.Date, renderedSection)
	} else {
		return fmt.Errorf("failed reading existing daily note: %w", err)
	}

	tempPath := filepath.Join(dailyDir, fmt.Sprintf(".%s.md.tmp.%d", record.Date, time.Now().UnixNano()))
	if err := os.WriteFile(tempPath, []byte(fullContent), FilePermPrivate); err != nil {
		return fmt.Errorf("failed writing temporary obsidian note: %w", err)
	}

	if err := os.Rename(tempPath, notePath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed renaming temporary obsidian note: %w", err)
	}

	return nil
}

func replaceOrAppendTimesheetSection(content string, renderedSection string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inSection := false
	foundSection := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "## Clustered timesheet") {
			inSection = true
			foundSection = true
			result = append(result, strings.TrimRight(renderedSection, "\n"))
			continue
		}
		if inSection {
			if strings.HasPrefix(line, "## ") {
				inSection = false
				result = append(result, line)
			}
			continue
		}
		result = append(result, line)
	}

	if !foundSection {
		trimmed := strings.TrimRight(content, "\n")
		if trimmed == "" {
			return renderedSection
		}
		return trimmed + "\n\n" + renderedSection
	}

	return strings.Join(result, "\n")
}
