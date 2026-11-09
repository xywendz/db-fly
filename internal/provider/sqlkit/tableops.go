package sqlkit

import (
	"context"

	"github.com/db-fly/db-fly/internal/domain"
)

// TableOps 是轻量版 domain.TableMaintenance，供已有 DDL 实现的模块复用。
type TableOps struct {
	Name      string
	Qualified func(ref domain.ObjectRef) (string, error)
	// Statements 按动作生成完整语句列表（可包含 USE [db] 等上下文语句）。
	Statements func(action domain.TableAction, ref domain.ObjectRef, qualified string) ([]string, error)
	// Database 可选，用于按库分池的模块。
	Database func(ref domain.ObjectRef) string
}

// TableActionScript 生成 TRUNCATE / DELETE FROM 的可预览脚本。
func (o TableOps) TableActionScript(_ context.Context, ref domain.ObjectRef, action domain.TableAction) (domain.Script, error) {
	if !action.Valid() {
		return domain.Script{}, domain.NewError(domain.CodeValidation, o.Name+".maintenance",
			"不支持的清空动作 %q（可用 truncate/empty）", action)
	}
	qualified, err := o.Qualified(ref)
	if err != nil {
		return domain.Script{}, err
	}
	statements, err := o.Statements(action, ref, qualified)
	if err != nil {
		return domain.Script{}, err
	}
	var warnings []string
	switch action {
	case domain.TableActionTruncate:
		warnings = []string{
			"TRUNCATE 会删除表中全部数据且不写入逐行日志，通常隐式提交、不可回滚。",
			"多数数据库会同时重置自增序列；有外键引用时可能被拒绝。",
		}
	case domain.TableActionEmpty:
		warnings = []string{
			"DELETE FROM 会逐行删除全部数据，可放入事务但在大表上较慢。",
			"不会重置自增序列，也不会释放表空间；操作不可自动回滚。",
		}
	}
	script := domain.Script{
		Format: "sql", Statements: statements, Warnings: warnings,
	}
	if o.Database != nil {
		script.Database = o.Database(ref)
	}
	return script, nil
}
