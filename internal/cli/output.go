package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/platform/export"
)

func readSQL(sqlText, file string, useStdin bool) (string, error) {
	if strings.TrimSpace(sqlText) != "" {
		return sqlText, nil
	}
	if file != "" {
		b, err := os.ReadFile(file)
		return string(b), err
	}
	if !useStdin {
		if stat, err := os.Stdin.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
			useStdin = true
		}
	}
	if !useStdin {
		return "", fmt.Errorf("请通过 --sql / --file 提供查询文本，或使用 --stdin 从标准输入读取")
	}
	b, err := io.ReadAll(os.Stdin)
	return string(b), err
}

func (c *cli) writeOutcomes(cmd *cobra.Command, outcomes []domain.QueryOutcome, format, output string) error {
	if format == "csv" {
		return writeCSVOutcomes(cmd, outcomes, output)
	}
	w := cmd.OutOrStdout()
	if output != "" {
		f, err := os.Create(output)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	switch format {
	case "json":
		if len(outcomes) == 1 {
			return export.JSON(w, outcomes[0])
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(outcomes)
	default:
		return writeTableOutcomes(w, outcomes)
	}
}

// writeCSVOutcomes 支持多结果集：指定 --output 时按结果集写多个文件，
// 否则顺序输出到同一流并用注释行分隔（json 格式可无损保留全部结果集与原始文档）。
func writeCSVOutcomes(cmd *cobra.Command, outcomes []domain.QueryOutcome, output string) error {
	pages := make([]domain.ResultPage, 0)
	for _, o := range outcomes {
		for _, p := range o.Pages {
			if len(p.Columns) > 0 {
				pages = append(pages, p)
			}
		}
	}
	if len(pages) == 0 {
		return nil
	}
	if output != "" && len(pages) > 1 {
		ext := filepath.Ext(output)
		base := strings.TrimSuffix(output, ext)
		for i, p := range pages {
			path := fmt.Sprintf("%s_%d%s", base, i+1, ext)
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			writeErr := export.CSV(f, p)
			closeErr := f.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "已导出结果集 %d → %s\n", i+1, path)
		}
		return nil
	}
	w := cmd.OutOrStdout()
	if output != "" {
		f, err := os.Create(output)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	for i, p := range pages {
		if i > 0 {
			fmt.Fprintf(w, "# --- 结果集 %d ---\n", i+1)
		}
		if err := export.CSV(w, p); err != nil {
			return err
		}
	}
	return nil
}

func writeTableOutcomes(w io.Writer, outcomes []domain.QueryOutcome) error {
	for i, o := range outcomes {
		if len(outcomes) > 1 {
			fmt.Fprintf(w, "--- 语句 %d ---\n", i+1)
		}
		for j, p := range o.Pages {
			if len(o.Pages) > 1 {
				fmt.Fprintf(w, "--- 结果集 %d ---\n", j+1)
			}
			if err := export.Table(w, p); err != nil {
				return err
			}
			if len(p.JSONDocuments) > 0 {
				fmt.Fprintf(w, "（结果集 %d 含 %d 个原始 JSON 文档，可用 --format json 导出）\n",
					j+1, len(p.JSONDocuments))
			}
		}
		for _, m := range o.Summary.Messages {
			fmt.Fprintf(w, "[%s] %s\n", m.Level, m.Text)
		}
		if o.Summary.Affected > 0 {
			fmt.Fprintf(w, "影响 %d 行\n", o.Summary.Affected)
		}
		truncated := ""
		if o.Summary.Truncated {
			truncated = " · 已达行数上限，结果被截断"
		}
		fmt.Fprintf(w, "%d 行 · %d ms%s\n", o.Summary.Rows, o.Summary.DurationMs, truncated)
	}
	return nil
}

// printScript 输出可预览的方言脚本（SQL 或 MongoDB JSON 命令），含警告与回滚。
func printScript(w io.Writer, script domain.Script) {
	comment := "--"
	if script.Format == "mongo_command" {
		comment = "//"
	}
	for i, stmt := range script.Statements {
		if script.Format == "mongo_command" {
			fmt.Fprintf(w, "%s 命令 %d\n%s\n", comment, i+1, stmt)
		} else {
			fmt.Fprintf(w, "%s 语句 %d\n%s;\n", comment, i+1, stmt)
		}
	}
	for _, warn := range script.Warnings {
		fmt.Fprintf(w, "%s 警告: %s\n", comment, warn)
	}
	if len(script.Rollback) > 0 {
		fmt.Fprintf(w, "%s 回滚脚本（best-effort，执行前请人工复核）\n", comment)
		for _, stmt := range script.Rollback {
			fmt.Fprintf(w, "%s %s\n", comment, stmt)
		}
	}
}

func (c *cli) previewAndApply(cmd *cobra.Command, conn string, script domain.Script, apply, yes bool) error {
	out := cmd.OutOrStdout()
	printScript(out, script)
	if len(script.Statements) == 0 {
		fmt.Fprintln(out, "没有需要执行的语句")
		return nil
	}
	if !apply {
		fmt.Fprintln(out, "预览模式，未执行；确认后追加 --apply --yes")
		return nil
	}
	if !yes {
		return fmt.Errorf("执行结构变更需要 --yes 显式确认（脚本已打印，请复核后再执行）")
	}
	result, err := c.app.ApplyScript(cmd.Context(), conn, script)
	if err != nil {
		return err
	}
	for _, o := range result.Outcomes {
		fmt.Fprintf(out, "执行成功：%s（影响 %d 行）\n", o.Message, o.RowsAffected)
	}
	return nil
}

func loadJSON(path string, target any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("%s 格式错误: %w", path, err)
	}
	return nil
}
