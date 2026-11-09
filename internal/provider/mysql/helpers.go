package mysql

import (
	"database/sql"
	"regexp"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

var safeTokenRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// quoteIdent 用反引号包裹标识符，内部反引号双写转义。
func quoteIdent(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", domain.NewError(domain.CodeValidation, "mysql.identifier", "标识符不能为空")
	}
	if strings.ContainsRune(name, 0) || len(name) > 128 {
		return "", domain.NewError(domain.CodeValidation, "mysql.identifier", "非法标识符 %q", name)
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

// quoteQualified 生成 `db`.`table` 形式的限定名。
func quoteQualified(database, name string) (string, error) {
	n, err := quoteIdent(name)
	if err != nil {
		return "", err
	}
	if database == "" {
		return n, nil
	}
	d, err := quoteIdent(database)
	if err != nil {
		return "", err
	}
	return d + "." + n, nil
}

// safeToken 校验字符集名、排序规则名、引擎名等只能由安全字符组成。
func safeToken(s, field string) error {
	if s == "" {
		return nil
	}
	if !safeTokenRe.MatchString(s) || len(s) > 64 {
		return domain.NewError(domain.CodeValidation, "mysql.script", "%s 含非法字符: %q", field, s)
	}
	return nil
}

// validateFragment 校验由 UI 结构化表单拼入 DDL 的片段（字段类型、默认值等）。
func validateFragment(s, field string) error {
	if len(s) > 512 {
		return domain.NewError(domain.CodeValidation, "mysql.script", "%s 过长", field)
	}
	for _, bad := range []string{";", "--", "/*", "*/", "`", "\x00"} {
		if strings.Contains(s, bad) {
			return domain.NewError(domain.CodeValidation, "mysql.script", "%s 含不允许的字符 %q", field, bad)
		}
	}
	return nil
}

func escapeLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// scanAllStrings 把任意结果集读成字符串矩阵，NULL 表示为空串。
func scanAllStrings(rows *sql.Rows) ([][]string, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]string
	for rows.Next() {
		values := make([]any, len(cols))
		raws := make([]*sql.RawBytes, len(cols))
		for i := range raws {
			raws[i] = new(sql.RawBytes)
			values[i] = raws[i]
		}
		if err := rows.Scan(values...); err != nil {
			return nil, err
		}
		record := make([]string, len(cols))
		for i, p := range raws {
			if *p != nil {
				record[i] = string(*p)
			}
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
