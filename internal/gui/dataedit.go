//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	core "github.com/dbclient/dbclient/internal/app"
	"github.com/dbclient/dbclient/internal/domain"
)

const (
	actionUpdate = "更新当前行"
	actionDelete = "删除当前行"
	actionInsert = "复制为新行"
)

// showRowEditor 从结果网格中选中的行构造结构化编辑请求，
// 严格遵循「编辑表单 → 脚本预览 → 显式确认 → 回写」流程。
func showRowEditor(u *ui, conn, database, table string, grid *resultGrid, refresh func()) {
	if conn == "" || table == "" {
		u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	if !u.capsFor(conn).Has(domain.CapDataEdit) {
		u.showInfo("提示", "当前连接的模块未声明数据编辑能力")
		return
	}
	page, rowIndex, ok := grid.selection()
	if !ok {
		u.showInfo("提示", "请先在结果表格中选中一行数据（不要选表头）")
		return
	}
	original := make(map[string]string, len(page.Columns))
	for i, col := range page.Columns {
		if i < len(page.Rows[rowIndex]) {
			original[col.Name] = page.Rows[rowIndex][i].Display()
		}
	}
	ref := domain.ObjectRef{Database: database, Name: table}
	u.async(func() {
		keys, err := u.core.TableKeyColumns(context.Background(), conn, ref)
		fyne.Do(func() {
			if err != nil {
				keys = nil
				u.setStatus("未推断出主键，请在编辑框中手动填写键列: %s", domain.UserMessage(err))
			}
			openRowEditorDialog(u, conn, ref, keys, page.Columns, original, refresh)
		})
	})
}

func openRowEditorDialog(u *ui, conn string, ref domain.ObjectRef, keys []string,
	columns []domain.Column, original map[string]string, refresh func()) {

	action := widget.NewSelect([]string{actionUpdate, actionDelete, actionInsert}, nil)
	action.SetSelected(actionUpdate)
	keyEntry := widget.NewEntry()
	keyEntry.SetText(strings.Join(keys, ", "))
	keyEntry.SetPlaceHolder("定位原始行的键列，逗号分隔")

	valuesBox := container.NewVBox()
	entries := make(map[string]*widget.Entry, len(columns))
	for _, col := range columns {
		entry := widget.NewEntry()
		entry.SetText(original[col.Name])
		entries[col.Name] = entry
		valuesBox.Add(container.NewGridWithColumns(2, widget.NewLabel(col.Name), entry))
	}
	head := container.NewVBox(action, widget.NewLabel("键列（原始值）"), keyEntry)
	content := container.NewGridWrap(fyne.NewSize(620, 400),
		container.NewBorder(head, nil, nil, nil, container.NewVScroll(valuesBox)))

	dialog.ShowCustomConfirm("编辑数据行", "生成预览", "取消", content, func(ok bool) {
		if !ok {
			return
		}
		kind := editActionKind(action.Selected)
		keyColumns := splitList(keyEntry.Text)
		values := make(map[string]*string, len(entries))
		for name, entry := range entries {
			// 更新只提交被改动的列；插入则复制整行（去掉 _id 避免主键冲突）。
			if kind != domain.EditInsert && entry.Text == original[name] {
				continue
			}
			text := entry.Text
			if text == "NULL" {
				values[name] = nil
			} else {
				value := text
				values[name] = &value
			}
		}
		edit := domain.RowEdit{Kind: kind, Values: values}
		if kind == domain.EditInsert {
			delete(values, "_id")
		} else {
			key := make(map[string]*string, len(keyColumns))
			for _, column := range keyColumns {
				originalValue, exists := original[column]
				if !exists {
					u.showInfo("提示", "结果集中找不到键列 "+column)
					return
				}
				if originalValue == "NULL" {
					key[column] = nil
				} else {
					value := originalValue
					key[column] = &value
				}
			}
			if len(key) == 0 {
				u.showInfo("提示", "更新或删除必须至少指定一个键列")
				return
			}
			edit.Key = key
		}
		if kind == domain.EditDelete {
			edit.Values = nil
		}
		req := domain.EditRequest{
			Ref: ref, KeyColumns: keyColumns,
			Edits: []domain.RowEdit{edit}, Transaction: true,
		}
		u.async(func() {
			previews, err := u.core.PreviewDataEdit(context.Background(), conn, req)
			fyne.Do(func() {
				if err != nil {
					u.showError(err)
					return
				}
				dialog.NewConfirm("确认执行数据回写",
					strings.Join(previews, "\n---\n"), func(confirm bool) {
						if !confirm {
							return
						}
						u.async(func() {
							result, err := u.core.ApplyDataEdit(context.Background(), conn, req)
							fyne.Do(func() {
								if err != nil {
									u.showError(err)
									return
								}
								u.showInfo("完成", fmt.Sprintf("已应用 %d 条变更", result.Applied))
								if refresh != nil {
									refresh()
								}
							})
						})
					}, u.win).Show()
			})
		})
	}, u.win)
}

func editActionKind(selected string) domain.EditKind {
	switch selected {
	case actionDelete:
		return domain.EditDelete
	case actionInsert:
		return domain.EditInsert
	default:
		return domain.EditUpdate
	}
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// showImportDialog 先用 dry-run 展示将要生成的参数化语句，确认后再真正写入。
func showImportDialog(u *ui, conn, database, table string, refresh func()) {
	if conn == "" || table == "" {
		u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			u.showError(err)
			return
		}
		if reader == nil {
			return
		}
		path := reader.URI().Path()
		_ = reader.Close()
		ref := domain.ObjectRef{Database: database, Name: table}
		u.async(func() {
			preview, err := u.core.ImportFile(context.Background(), conn, ref, path,
				core.ImportOptions{HasHeader: true, DryRun: true})
			fyne.Do(func() {
				if err != nil {
					u.showError(err)
					return
				}
				message := fmt.Sprintf("文件: %s\n格式: %s\n数据行: %d\n生成语句: %d\n\n%s",
					path, preview.Format, preview.Rows, preview.Statements,
					strings.Join(preview.Preview, "\n---\n"))
				dialog.NewConfirm("确认导入数据", message, func(ok bool) {
					if !ok {
						return
					}
					u.async(func() {
						result, err := u.core.ImportFile(context.Background(), conn, ref, path,
							core.ImportOptions{HasHeader: true, DryRun: false})
						fyne.Do(func() {
							if err != nil {
								u.showError(err)
								return
							}
							u.showInfo("完成", fmt.Sprintf("已写入 %d 条", result.Result.Applied))
							if refresh != nil {
								refresh()
							}
						})
					})
				}, u.win).Show()
			})
		})
	}, u.win)
}
