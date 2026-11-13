package bridge

import "github.com/db-fly/db-fly/internal/domain"

// PreviewCreateDatabase 生成建库脚本（仅预览，未执行）。
func (b *Bridge) PreviewCreateDatabase(conn string, spec domain.DatabaseSpec) (domain.Script, error) {
	return b.app.PreviewCreateDatabase(b.ctx(), conn, spec)
}

// PreviewAlterDatabase 生成改库脚本（仅预览）。
func (b *Bridge) PreviewAlterDatabase(conn, name string, spec domain.DatabaseSpec) (domain.Script, error) {
	return b.app.PreviewAlterDatabase(b.ctx(), conn, name, spec)
}

// PreviewCreateTable 生成建表脚本（仅预览）。
func (b *Bridge) PreviewCreateTable(conn string, def domain.TableDefinition) (domain.Script, error) {
	return b.app.PreviewCreateTable(b.ctx(), conn, def)
}

// PreviewAlterTable 生成表结构变更脚本（仅预览）。
func (b *Bridge) PreviewAlterTable(conn string, ref domain.ObjectRef, changes []domain.SchemaChange) (domain.Script, error) {
	return b.app.PreviewAlterTable(b.ctx(), conn, ref, changes)
}

// PreviewRenameTable 生成重命名表的脚本（仅预览）。
func (b *Bridge) PreviewRenameTable(conn string, ref domain.ObjectRef, newName string) (domain.Script, error) {
	return b.app.PreviewRenameTable(b.ctx(), conn, ref, newName)
}

// PreviewDropObject 生成删除对象的脚本（仅预览）。
func (b *Bridge) PreviewDropObject(conn string, ref domain.ObjectRef) (domain.Script, error) {
	return b.app.PreviewDropObject(b.ctx(), conn, ref)
}

// ShowCreateObject 返回对象的建表 SQL（或等价创建命令）。
func (b *Bridge) ShowCreateObject(conn string, ref domain.ObjectRef) (string, error) {
	return b.app.ShowCreate(b.ctx(), conn, ref)
}

// PreviewTableAction 生成 TRUNCATE / 清空表数据脚本（仅预览）。
func (b *Bridge) PreviewTableAction(conn string, ref domain.ObjectRef, action domain.TableAction) (domain.Script, error) {
	return b.app.PreviewTableAction(b.ctx(), conn, ref, action)
}

// ApplyScript 执行已确认的结构变更脚本。只读连接会被拒绝。
func (b *Bridge) ApplyScript(conn string, script domain.Script) (domain.ApplyResult, error) {
	return b.app.ApplyScript(b.ctx(), conn, script)
}

// PreviewCopyTable 生成「复制表结构 / 复制表结构和数据」的脚本（仅预览）。
func (b *Bridge) PreviewCopyTable(conn string, req domain.CopyTableRequest) (domain.Script, error) {
	return b.app.PreviewCopyTable(b.ctx(), conn, req)
}

// ExportDatabase 生成整个数据库的 SQL 导出脚本（建表语句 + 数据 INSERT）。
func (b *Bridge) ExportDatabase(conn, database string, structure, data bool, maxRows int) (string, error) {
	return b.app.ExportDatabase(b.ctx(), conn, database, structure, data, maxRows)
}
