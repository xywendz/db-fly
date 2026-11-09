package app

import (
	"context"
	"strings"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/platform/audit"
)

// 结果集编辑统一走「结构化变更 → 模块生成参数化脚本 → 预览确认 → 回写」。
// app 层不感知任何数据库方言，也不拼接用户值。

func (a *App) dataSession(ctx context.Context, conn string) (domain.Session, domain.ConnectionConfig, error) {
	c, err := a.cfgOf(conn)
	if err != nil {
		return nil, c, err
	}
	s, err := a.Session(ctx, conn)
	if err != nil {
		return nil, c, err
	}
	if !s.Capabilities().Has(domain.CapDataEdit) {
		return nil, c, domain.NewError(domain.CodeUnsupported, "data_edit",
			"连接 %q 所属模块未声明数据编辑能力", c.Name)
	}
	return s, c, nil
}

func validateEditRequest(req domain.EditRequest) error {
	if req.Ref.Name == "" {
		return domain.NewError(domain.CodeValidation, "data_edit", "未指定目标表或集合")
	}
	if len(req.Edits) == 0 {
		return domain.NewError(domain.CodeValidation, "data_edit", "没有待提交的变更")
	}
	for i, edit := range req.Edits {
		switch edit.Kind {
		case domain.EditUpdate:
			if len(edit.Key) == 0 {
				return domain.NewError(domain.CodeValidation, "data_edit",
					"第 %d 条更新缺少主键值", i+1)
			}
			if len(edit.Values) == 0 {
				return domain.NewError(domain.CodeValidation, "data_edit",
					"第 %d 条更新内容为空", i+1)
			}
		case domain.EditDelete:
			if len(edit.Key) == 0 {
				return domain.NewError(domain.CodeValidation, "data_edit",
					"第 %d 条删除缺少主键值", i+1)
			}
		case domain.EditInsert:
			if len(edit.Values) == 0 {
				return domain.NewError(domain.CodeValidation, "data_edit",
					"第 %d 条插入内容为空", i+1)
			}
		default:
			return domain.NewError(domain.CodeUnsupported, "data_edit",
				"不支持的编辑类型 %q", edit.Kind)
		}
	}
	return nil
}

// PreviewDataEdit 返回由各数据库模块生成的可读写入脚本/命令，不修改数据。
func (a *App) PreviewDataEdit(ctx context.Context, conn string, req domain.EditRequest) ([]string, error) {
	if err := validateEditRequest(req); err != nil {
		return nil, err
	}
	s, _, err := a.dataSession(ctx, conn)
	if err != nil {
		return nil, err
	}
	return s.Data().Preview(ctx, req)
}

// ApplyDataEdit 执行已经过预览确认的数据变更。只读连接一律拒绝。
func (a *App) ApplyDataEdit(ctx context.Context, conn string, req domain.EditRequest) (domain.EditResult, error) {
	if err := validateEditRequest(req); err != nil {
		return domain.EditResult{}, err
	}
	s, c, err := a.dataSession(ctx, conn)
	if err != nil {
		return domain.EditResult{}, err
	}
	if c.ReadOnly {
		return domain.EditResult{}, domain.NewError(domain.CodeReadOnly, "data_edit",
			"只读连接禁止回写数据")
	}
	result, err := s.Data().Apply(ctx, req)
	a.Audit.Log(audit.Entry{
		Connection: c.Name, Kind: string(c.Kind), Action: "data_edit",
		Statement: strings.Join(result.Statements, "\n"),
		Rows:      int64(result.Applied), OK: err == nil, Error: errorText(err),
	})
	return result, err
}

// TableKeyColumns 推断可安全定位一行数据的列：主键 → 唯一索引 → 文档 _id。
// 表现层应把它作为默认值展示给用户，并允许显式指定 --keys 覆盖。
func (a *App) TableKeyColumns(ctx context.Context, conn string, ref domain.ObjectRef) ([]string, error) {
	if ref.Name == "" {
		return nil, domain.NewError(domain.CodeValidation, "key_columns", "未指定目标表或集合")
	}
	s, err := a.Session(ctx, conn)
	if err != nil {
		return nil, err
	}
	if schema, describeErr := s.Metadata().Describe(ctx, ref); describeErr == nil {
		keys := make([]string, 0, len(schema.Columns))
		for _, col := range schema.Columns {
			if col.PrimaryKey {
				keys = append(keys, col.Name)
			}
		}
		if len(keys) == 0 {
			for _, idx := range schema.Indexes {
				if (idx.Unique || idx.Primary) && len(idx.Columns) > 0 {
					keys = append(keys, idx.Columns...)
					break
				}
			}
		}
		if len(keys) > 0 {
			return keys, nil
		}
	}
	if s.Capabilities().Has(domain.CapJSONTree) {
		return []string{"_id"}, nil
	}
	return nil, domain.NewError(domain.CodeValidation, "key_columns",
		"对象 %s 没有主键或唯一索引，无法安全定位行，请显式指定键列", ref.String())
}
