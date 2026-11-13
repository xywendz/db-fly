package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/export"
)

// ExportDatabase 生成整个数据库的 SQL 导出脚本（建表语句 + 数据 INSERT），
// 不记录查询历史。structure 控制是否包含建表语句，data 控制是否包含数据，
// maxRows 限制单个表导出的行数，防止大表撑爆内存。
func (a *App) ExportDatabase(ctx context.Context, conn, database string, structure, data bool, maxRows int) (string, error) {
	if !structure && !data {
		return "", domain.NewError(domain.CodeValidation, "export", "请至少选择导出结构或数据之一")
	}
	c, err := a.cfgOf(conn)
	if err != nil {
		return "", err
	}
	if !relationalKinds[string(c.Kind)] {
		return "", domain.NewError(domain.CodeUnsupported, "export", "%s 暂不支持整库导出（仅关系型数据库适用）", string(c.Kind))
	}
	if maxRows <= 0 {
		maxRows = 5000
	}
	if maxRows > 500000 {
		maxRows = 500000
	}
	s, err := a.Session(ctx, conn)
	if err != nil {
		return "", err
	}
	objs, err := s.Metadata().ListObjects(ctx, database, "")
	if err != nil {
		return "", err
	}
	tables := make([]string, 0)
	for _, o := range objs {
		if o.Type == domain.ObjectTable {
			tables = append(tables, o.Name)
		}
	}
	if len(tables) == 0 {
		return "", domain.NewError(domain.CodeNotFound, "export", "该数据库下没有可导出的表")
	}

	var b strings.Builder
	quote := func(name string) string { return quoteIdentByKind(string(c.Kind), name) }
	for _, t := range tables {
		ref := domain.ObjectRef{Database: database, Name: t}
		if structure {
			ddl, derr := s.Schema().ShowCreate(ctx, ref)
			if derr != nil {
				return "", domain.WrapError(domain.CodeQuery, "export", derr, "读取表 %s 结构失败", t)
			}
			b.WriteString(fmt.Sprintf("-- ===== 表 %s =====\n", t))
			b.WriteString(ddl)
			if !strings.HasSuffix(strings.TrimSpace(ddl), ";") {
				b.WriteString(";")
			}
			b.WriteString("\n\n")
		}
		if data {
			if !structure {
				b.WriteString(fmt.Sprintf("-- ===== 表 %s 数据 =====\n", t))
			}
			q := domain.Query{
				Language: domain.LangSQL,
				Ref:      ref,
				MaxRows:  maxRows,
				ReadOnly: true,
			}
			execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			rs, rsErr := s.Query().Execute(execCtx, q)
			if rsErr != nil {
				cancel()
				return "", domain.WrapError(domain.CodeQuery, "export", rsErr, "读取表 %s 数据失败", t)
			}
			page, perr := collectExportPage(rs, maxRows)
			cancel()
			if perr != nil {
				return "", perr
			}
			if err := export.SQL(&b, t, page, quote); err != nil {
				return "", err
			}
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// collectExportPage 把流式结果集收集成单页，施加 maxRows 限制，不记录历史/审计。
func collectExportPage(rs domain.ResultSet, maxRows int) (domain.ResultPage, error) {
	defer rs.Close()
	page := domain.ResultPage{Columns: rs.Columns(), Rows: []domain.Row{}}
	if page.Columns == nil {
		page.Columns = []domain.Column{}
	}
	for rs.Next() {
		if maxRows > 0 && len(page.Rows) >= maxRows {
			page.Truncated = true
			break
		}
		page.Rows = append(page.Rows, rs.Row())
	}
	return page, rs.Err()
}
