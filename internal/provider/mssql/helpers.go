package mssql

import (
	"regexp"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

var (
	safeTokenRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	goLine      = regexp.MustCompile(`(?i)^\s*GO\s*(--.*)?$`)
)

// quoteIdent 使用 T-SQL 的方括号标识符，内部的 ] 双写转义。
func quoteIdent(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", domain.NewError(domain.CodeValidation, "sqlserver.identifier", "标识符不能为空")
	}
	if strings.ContainsRune(name, 0) || len(name) > 128 {
		return "", domain.NewError(domain.CodeValidation, "sqlserver.identifier", "非法标识符 %q", name)
	}
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]", nil
}

// quoteQualified 生成 [db].[schema].[table] 形式的限定名。
func quoteQualified(database, schema, name string) (string, error) {
	n, err := quoteIdent(name)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, 3)
	if database != "" {
		d, err := quoteIdent(database)
		if err != nil {
			return "", err
		}
		parts = append(parts, d)
	}
	if schema != "" {
		s, err := quoteIdent(schema)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	parts = append(parts, n)
	return strings.Join(parts, "."), nil
}

func safeToken(s, field string) error {
	if s == "" {
		return nil
	}
	if !safeTokenRe.MatchString(s) || len(s) > 128 {
		return domain.NewError(domain.CodeValidation, "sqlserver.script", "%s 含非法字符: %q", field, s)
	}
	return nil
}

func validateFragment(s, field string) error {
	if len(s) > 1000 {
		return domain.NewError(domain.CodeValidation, "sqlserver.script", "%s 过长", field)
	}
	for _, bad := range []string{";", "--", "/*", "*/", "[", "]", "\x00"} {
		if strings.Contains(s, bad) {
			return domain.NewError(domain.CodeValidation, "sqlserver.script", "%s 含不允许的字符 %q", field, bad)
		}
	}
	return nil
}

func escapeLiteral(s string) string { return strings.ReplaceAll(s, "'", "''") }

// splitBatches 按 GO 分隔符拆分 T-SQL 脚本。
// GO 是客户端工具约定的批分隔符，SQL Server 驱动并不识别，必须由本模块处理。
func splitBatches(text string) []string {
	var (
		out []string
		buf strings.Builder
	)
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if goLine.MatchString(line) {
			if s := strings.TrimSpace(buf.String()); s != "" {
				out = append(out, s)
			}
			buf.Reset()
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// firstKeyword 提取批中首个有效关键字（跳过注释）。
func firstKeyword(batch string) string {
	text := batch
	for {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "/*") {
			if end := strings.Index(trimmed, "*/"); end >= 0 {
				text = trimmed[end+2:]
				continue
			}
		}
		if strings.HasPrefix(trimmed, "--") {
			if end := strings.IndexByte(trimmed, '\n'); end >= 0 {
				text = trimmed[end+1:]
				continue
			}
			return ""
		}
		fields := strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '('
		})
		if len(fields) == 0 {
			return ""
		}
		return strings.ToUpper(fields[0])
	}
}

// isResultBatch 判断该批是否可能返回结果集，决定用 QueryContext 还是 ExecContext。
func isResultBatch(batch string) bool {
	switch firstKeyword(batch) {
	case "SELECT", "WITH", "EXEC", "EXECUTE", "PRINT", "DBCC", "SHOWPLAN", "DECLARE", "SET":
		return true
	default:
		return false
	}
}

func defaultSchema(schema string) string {
	if strings.TrimSpace(schema) == "" {
		return "dbo"
	}
	return schema
}
