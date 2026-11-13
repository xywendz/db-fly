package bridge

import "github.com/db-fly/db-fly/internal/domain"

// PreviewDataEdit 返回数据回写的参数化语句/命令（仅预览，不修改数据）。
func (b *Bridge) PreviewDataEdit(conn string, req domain.EditRequest) ([]string, error) {
	return b.app.PreviewDataEdit(b.ctx(), conn, req)
}

// ApplyDataEdit 执行已确认的数据变更（参数化回写）。只读连接会被拒绝。
func (b *Bridge) ApplyDataEdit(conn string, req domain.EditRequest) (domain.EditResult, error) {
	return b.app.ApplyDataEdit(b.ctx(), conn, req)
}

// TableKeyColumns 推断可安全定位行的列（主键 → 唯一索引 → _id），供前端默认键列。
func (b *Bridge) TableKeyColumns(conn string, ref domain.ObjectRef) ([]string, error) {
	return b.app.TableKeyColumns(b.ctx(), conn, ref)
}
