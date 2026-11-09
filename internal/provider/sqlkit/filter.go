package sqlkit

import (
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// FilterSort 是表数据浏览时附加的 WHERE / ORDER BY 片段（均不含关键字本身）。
type FilterSort struct {
	Where   string
	OrderBy string
}

func stripKeyword(text, keyword string) string {
	if len(text) >= len(keyword) && strings.EqualFold(text[:len(keyword)], keyword) {
		return strings.TrimSpace(text[len(keyword):])
	}
	return text
}

// ParseFilterSort 解析并校验用户输入的表浏览过滤/排序条件。
//
//   - filter: SQL WHERE 表达式，如 "age > 18 AND status = 'A'"；也允许带 WHERE 前缀。
//   - sort:   逗号分隔的排序项，如 "created_at DESC, id ASC"；仅允许标识符与 ASC/DESC。
//
// 过滤条件只做危险片段校验（禁止注释与语句分隔符），表达式由数据库自身解析；
// 排序条件中的标识符一律按方言引用，避免拼接注入。
func ParseFilterSort(filter, sortText string, q Quoter) (FilterSort, error) {
	out := FilterSort{}
	condition := stripKeyword(strings.TrimSpace(filter), "WHERE")
	if condition != "" {
		if err := ValidateFragment(condition, "过滤条件"); err != nil {
			return out, err
		}
		out.Where = condition
	}
	order := stripKeyword(strings.TrimSpace(sortText), "ORDER BY")
	if order != "" {
		parsed, err := ParseOrderBy(order, q)
		if err != nil {
			return out, err
		}
		out.OrderBy = parsed
	}
	return out, nil
}

// ParseOrderBy 把 "col DESC, t.id ASC" 转成方言引用后的 "col DESC, \"t\".\"id\" ASC"。
func ParseOrderBy(text string, q Quoter) (string, error) {
	if q.Open == "" || q.Close == "" {
		q = ANSIQuoter
	}
	if err := ValidateFragment(text, "排序条件"); err != nil {
		return "", err
	}
	terms := strings.Split(text, ",")
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		fields := strings.Fields(strings.TrimSpace(term))
		if len(fields) == 0 {
			return "", domain.NewError(domain.CodeValidation, "sqlkit.sort", "排序条件中存在空项")
		}
		if len(fields) > 2 {
			return "", domain.NewError(domain.CodeValidation, "sqlkit.sort",
				"排序项 %q 只支持「字段 [ASC|DESC]」", strings.TrimSpace(term))
		}
		direction := ""
		if len(fields) == 2 {
			direction = strings.ToUpper(fields[1])
			if direction != "ASC" && direction != "DESC" {
				return "", domain.NewError(domain.CodeValidation, "sqlkit.sort",
					"排序方向 %q 无效（可用 ASC/DESC）", fields[1])
			}
		}
		names := strings.Split(fields[0], ".")
		for _, name := range names {
			if !identRe.MatchString(name) {
				return "", domain.NewError(domain.CodeValidation, "sqlkit.sort",
					"排序列 %q 不是合法标识符", fields[0])
			}
		}
		quoted, err := q.Qualified(names...)
		if err != nil {
			return "", err
		}
		if direction != "" {
			quoted += " " + direction
		}
		parts = append(parts, quoted)
	}
	return strings.Join(parts, ", "), nil
}

// SelectDialect 描述不同方言生成表数据预览 SELECT 的差异。
type SelectDialect struct {
	Quoter Quoter
	// Qualified 把 ObjectRef 转成方言限定名；不可跨库查询的方言应忽略 Database 层级。
	Qualified func(ref domain.ObjectRef) (string, error)
	// TopPrefix 生成 SELECT 之后、* 之前的行数限制（SQL Server 的 TOP n）。
	TopPrefix func(limit int) string
	// LimitSuffix 生成语句末尾的行数限制（LIMIT n / FETCH FIRST n ROWS ONLY）。
	LimitSuffix func(limit int) string
}

// BuildPreview 生成表数据浏览语句，或原样返回用户自定义 SQL。
// 自定义 SQL 与 filter/sort 不能同时使用，避免条件被静默忽略。
func BuildPreview(d SelectDialect, query domain.Query) (string, error) {
	if query.Language != "" && query.Language != domain.LangSQL {
		return "", domain.NewError(domain.CodeUnsupported, "sqlkit.query",
			"该模块只支持 SQL，收到 %q", query.Language)
	}
	if strings.TrimSpace(query.Text) != "" {
		if strings.TrimSpace(query.Filter) != "" || strings.TrimSpace(query.Sort) != "" {
			return "", domain.NewError(domain.CodeValidation, "sqlkit.query",
				"过滤/排序条件仅用于表数据浏览，不能与自定义 SQL 同时使用")
		}
		return strings.TrimSpace(query.Text), nil
	}
	if query.Ref.Name == "" {
		return "", domain.NewError(domain.CodeValidation, "sqlkit.query", "查询内容不能为空")
	}
	if d.Qualified == nil {
		return "", domain.NewError(domain.CodeValidation, "sqlkit.query", "未配置限定名生成器")
	}
	qualified, err := d.Qualified(query.Ref)
	if err != nil {
		return "", err
	}
	if d.Quoter.Open == "" || d.Quoter.Close == "" {
		d.Quoter = ANSIQuoter
	}
	fs, err := ParseFilterSort(query.Filter, query.Sort, d.Quoter)
	if err != nil {
		return "", err
	}
	limit := query.MaxRows
	if limit <= 0 {
		limit = domain.DefaultMaxRows
	}

	var b strings.Builder
	b.WriteString("SELECT ")
	if d.TopPrefix != nil {
		b.WriteString(d.TopPrefix(limit))
	}
	b.WriteString("* FROM ")
	b.WriteString(qualified)
	if fs.Where != "" {
		b.WriteString(" WHERE ")
		b.WriteString(fs.Where)
	}
	if fs.OrderBy != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(fs.OrderBy)
	}
	if d.LimitSuffix != nil {
		b.WriteString(d.LimitSuffix(limit))
	}
	return b.String(), nil
}
