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

// BuildConditions 把结构化过滤条件转成方言安全的 WHERE 片段（不含 WHERE 关键字）。
// 标识符由 Quoter 引用，字面量统一转义，避免 GUI 拼接 SQL。
func BuildConditions(conditions []domain.FilterCondition, q Quoter) (string, error) {
	if len(conditions) == 0 {
		return "", nil
	}
	if q.Open == "" || q.Close == "" {
		q = ANSIQuoter
	}
	parts := make([]string, 0, len(conditions))
	for i, c := range conditions {
		if len(c.Value) > 1024 || strings.ContainsRune(c.Value, 0) {
			return "", domain.NewError(domain.CodeValidation, "sqlkit.filter",
				"条件 %q 的值过长或含非法字符", c.Column)
		}
		column, err := q.Qualified(strings.Split(strings.TrimSpace(c.Column), ".")...)
		if err != nil {
			return "", err
		}
		op := strings.ToUpper(strings.TrimSpace(c.Operator))
		if op == "" {
			op = "="
		}
		if !allowedOperator(op) {
			return "", domain.NewError(domain.CodeValidation, "sqlkit.filter",
				"不支持的操作符 %q", c.Operator)
		}
		condition := column
		switch op {
		case "IS NULL", "IS NOT NULL":
			condition += " " + op
		case "IN", "NOT IN":
			items := strings.Split(c.Value, ",")
			literals := make([]string, 0, len(items))
			for _, item := range items {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				literals = append(literals, quoteLiteral(item))
			}
			if len(literals) == 0 {
				return "", domain.NewError(domain.CodeValidation, "sqlkit.filter",
					"IN 条件 %q 需要至少一个值", c.Column)
			}
			condition += " " + op + " (" + strings.Join(literals, ", ") + ")"
		default:
			value := strings.TrimSpace(c.Value)
			if value == "" {
				return "", domain.NewError(domain.CodeValidation, "sqlkit.filter",
					"条件 %q 需要填写值", c.Column)
			}
			if strings.EqualFold(value, "NULL") {
				condition += " " + op + " NULL"
			} else {
				condition += " " + op + " " + quoteLiteral(c.Value)
			}
		}
		if i > 0 {
			join := strings.ToUpper(strings.TrimSpace(c.Join))
			if join == "" {
				join = "AND"
			}
			if join != "AND" && join != "OR" {
				return "", domain.NewError(domain.CodeValidation, "sqlkit.filter",
					"条件连接词 %q 无效（只能 AND / OR）", c.Join)
			}
			condition = join + " " + condition
		}
		parts = append(parts, condition)
	}
	return strings.Join(parts, " "), nil
}

func allowedOperator(op string) bool {
	switch op {
	case "=", "<>", "!=", ">", ">=", "<", "<=", "LIKE", "NOT LIKE", "IN", "NOT IN", "IS NULL", "IS NOT NULL":
		return true
	default:
		return false
	}
}

func quoteLiteral(value string) string {
	if len(value) > 1024 || strings.ContainsRune(value, 0) {
		return "''"
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		value = value[1 : len(value)-1]
	}
	return "'" + EscapeLiteral(value) + "'"
}

// BuildOrderTerms 把结构化排序项转成方言引用后的 ORDER BY 片段。
func BuildOrderTerms(terms []domain.SortTerm, q Quoter) (string, error) {
	if len(terms) == 0 {
		return "", nil
	}
	if q.Open == "" || q.Close == "" {
		q = ANSIQuoter
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		column, err := q.Qualified(strings.Split(strings.TrimSpace(term.Column), ".")...)
		if err != nil {
			return "", err
		}
		if term.Desc {
			column += " DESC"
		} else {
			column += " ASC"
		}
		parts = append(parts, column)
	}
	return strings.Join(parts, ", "), nil
}

// SelectDialect 描述不同方言生成表数据预览 SELECT 的差异。
type SelectDialect struct {
	Quoter Quoter
	// Qualified 把 ObjectRef 转成方言限定名；不可跨库查询的方言应忽略 Database 层级。
	Qualified func(ref domain.ObjectRef) (string, error)
	// TopPrefix 生成 SELECT 之后、* 之前的行数限制（SQL Server 的 TOP n）；
	// offset > 0 时应返回空串，改用语句末尾的 OFFSET ... FETCH NEXT 分页。
	TopPrefix func(limit, offset int) string
	// LimitSuffix 生成语句末尾的行数限制与分页偏移。
	LimitSuffix func(limit, offset int) string
	// PageOrderBy 是 offset > 0 且用户未指定排序时的兜底排序（SQL Server 的 OFFSET 必须有 ORDER BY）。
	PageOrderBy string
}

// BuildPreview 生成表数据浏览语句，或原样返回用户自定义 SQL。
// 自定义 SQL 与 filter/sort 不能同时使用，避免条件被静默忽略。
func BuildPreview(d SelectDialect, query domain.Query) (string, error) {
	if query.Language != "" && query.Language != domain.LangSQL {
		return "", domain.NewError(domain.CodeUnsupported, "sqlkit.query",
			"该模块只支持 SQL，收到 %q", query.Language)
	}
	hasConditions := len(query.Conditions) > 0 || len(query.OrderTerms) > 0 || query.Offset > 0
	if strings.TrimSpace(query.Text) != "" {
		if strings.TrimSpace(query.Filter) != "" || strings.TrimSpace(query.Sort) != "" || hasConditions {
			return "", domain.NewError(domain.CodeValidation, "sqlkit.query",
				"过滤/排序/分页条件仅用于表数据浏览，不能与自定义 SQL 同时使用")
		}
		return strings.TrimSpace(query.Text), nil
	}
	if query.Ref.Name == "" {
		return "", domain.NewError(domain.CodeValidation, "sqlkit.query", "查询内容不能为空")
	}
	if query.Offset < 0 {
		return "", domain.NewError(domain.CodeValidation, "sqlkit.query", "分页偏移不能为负数")
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
	if len(query.Conditions) > 0 {
		where, err := BuildConditions(query.Conditions, d.Quoter)
		if err != nil {
			return "", err
		}
		fs.Where = where
	}
	if len(query.OrderTerms) > 0 {
		order, err := BuildOrderTerms(query.OrderTerms, d.Quoter)
		if err != nil {
			return "", err
		}
		fs.OrderBy = order
	}
	limit := query.MaxRows
	if limit <= 0 {
		limit = domain.DefaultMaxRows
	}

	var b strings.Builder
	b.WriteString("SELECT ")
	if d.TopPrefix != nil {
		b.WriteString(d.TopPrefix(limit, query.Offset))
	}
	b.WriteString("* FROM ")
	b.WriteString(qualified)
	if fs.Where != "" {
		b.WriteString(" WHERE ")
		b.WriteString(fs.Where)
	}
	switch {
	case fs.OrderBy != "":
		b.WriteString(" ORDER BY ")
		b.WriteString(fs.OrderBy)
	case query.Offset > 0 && d.PageOrderBy != "":
		b.WriteString(" ORDER BY ")
		b.WriteString(d.PageOrderBy)
	}
	if d.LimitSuffix != nil {
		b.WriteString(d.LimitSuffix(limit, query.Offset))
	}
	return b.String(), nil
}
