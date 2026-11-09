// Package sqlkit 提供关系型业务模块共用的、与具体数据库无关的基础件。
// 本包只依赖标准库与 domain，不是数据库业务模块，各 provider 均可复用。
package sqlkit

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
)

// Quoter 描述方言的标识符引用方式（ANSI 双引号、MySQL 反引号、T-SQL 方括号）。
type Quoter struct {
	Open  string
	Close string
}

var (
	ANSIQuoter  = Quoter{Open: `"`, Close: `"`}
	MySQLQuoter = Quoter{Open: "`", Close: "`"}
	TSQLQuoter  = Quoter{Open: "[", Close: "]"}
)

// Ident 引用单个标识符，内部的结束符双写转义。
func (q Quoter) Ident(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsRune(name, 0) || len(name) > 128 {
		return "", domain.NewError(domain.CodeValidation, "sqlkit.identifier", "非法标识符 %q", name)
	}
	if q.Open == "" || q.Close == "" {
		q = ANSIQuoter
	}
	return q.Open + strings.ReplaceAll(name, q.Close, q.Close+q.Close) + q.Close, nil
}

// Qualified 生成 a.b.c 形式的限定名，自动跳过空层级。
func (q Quoter) Qualified(parts ...string) (string, error) {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		// 已经被引用的标识符直接保留，避免 "public"."t" 被二次引用。
		if strings.HasPrefix(part, q.Open) && strings.HasSuffix(part, q.Close) && len(part) > len(q.Open)+len(q.Close)-1 {
			out = append(out, part)
			continue
		}
		quoted, err := q.Ident(part)
		if err != nil {
			return "", err
		}
		out = append(out, quoted)
	}
	if len(out) == 0 {
		return "", domain.NewError(domain.CodeValidation, "sqlkit.identifier", "缺少对象名")
	}
	return strings.Join(out, "."), nil
}

var (
	safeTokenRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
	identRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
)

// SafeToken 校验字符集、排序规则、引擎名等只由安全字符组成。
func SafeToken(s, field string) error {
	if s == "" {
		return nil
	}
	if !safeTokenRe.MatchString(s) || len(s) > 128 {
		return domain.NewError(domain.CodeValidation, "sqlkit.script", "%s 含非法字符: %q", field, s)
	}
	return nil
}

// ValidateFragment 校验由表单拼入 SQL 的片段（WHERE 条件、字段类型、默认值等）。
// 允许 SQL 表达式与字符串字面量，但禁止注释、语句分隔符与 NUL，防止拼出多条语句。
func ValidateFragment(s, field string) error {
	if len(s) > 1024 {
		return domain.NewError(domain.CodeValidation, "sqlkit.script", "%s 过长（最多 1024 字符）", field)
	}
	for _, bad := range []string{";", "--", "/*", "*/", "\x00"} {
		if strings.Contains(s, bad) {
			return domain.NewError(domain.CodeValidation, "sqlkit.script",
				"%s 含不允许的字符 %q", field, bad)
		}
	}
	return nil
}

func EscapeLiteral(s string) string { return strings.ReplaceAll(s, "'", "''") }

// Stringify 把驱动返回的任意值转为可展示文本，兼容 pgx / go-ora / modernc 的多种返回类型。
func Stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	case time.Time:
		return t.Format(time.RFC3339Nano)
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

// ScanAllStrings 把任意结果集读成字符串矩阵，NULL 表示为空串。
// 使用 *any 而非 *sql.RawBytes，保证在所有数据库驱动上都能扫描。
func ScanAllStrings(rows *sql.Rows) ([][]string, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		record := make([]string, len(cols))
		for i, v := range values {
			record[i] = Stringify(v)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// MapError 把驱动错误统一映射为领域错误，识别取消与超时。
func MapError(op string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case domain.CodeOf(err) == domain.CodeCanceled:
		return domain.WrapError(domain.CodeCanceled, op, err, "操作已取消")
	case domain.CodeOf(err) == domain.CodeTimeout:
		return domain.WrapError(domain.CodeTimeout, op, err, "操作超时")
	default:
		return domain.WrapError(domain.CodeQuery, op, err, "数据库操作失败")
	}
}
