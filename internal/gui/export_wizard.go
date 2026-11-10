//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/export"

	core "github.com/db-fly/db-fly/internal/app"
)

// exportWizard 是「导出向导」页签：按顺序选择 连接/库 → 对象 → 格式 → 路径 → 执行。
// 既支持单表导出，也支持整库导出（整库时每表一个文件放在选定的目录下）。
type exportWizard struct {
	u *ui

	step     int
	conn     string
	database string
	table    string
	wholeDB  bool
	format   string
	path     string
	maxRows  int
	perTable bool

	connSel, dbSel, tableSel *widget.Select
	wholeDBCheck             *widget.Check
	formatRad                *widget.RadioGroup
	pathEnt                  *widget.Entry
	maxRowsEnt               *widget.Entry
	perTableCheck            *widget.Check

	stepBar *fyne.Container
	body    *fyne.Container
	footer  *fyne.Container
	root    fyne.CanvasObject

	running bool
	log     *widget.Label
}

const (
	exportStepSource = iota
	exportStepObject
	exportStepFormat
	exportStepPath
	exportStepRun
	exportStepCount
)

var exportStepNames = []string{"选择源", "选择对象", "选择格式", "选择路径", "执行导出"}

func newExportWizard(u *ui) *exportWizard {
	return &exportWizard{u: u, maxRows: 100000, format: "sql"}
}

// build 构造向导骨架，之后由 goto 渲染各步骤内容。
func (w *exportWizard) build() fyne.CanvasObject {
	w.stepBar = container.NewHBox()
	w.body = container.NewMax()
	w.footer = container.NewHBox()
	w.root = container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("导出向导", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			w.stepBar, widget.NewSeparator(),
		),
		w.footer, nil, nil, w.body,
	)
	go w.loadConnections()
	w.goTo(exportStepSource)
	return w.root
}

func (w *exportWizard) loadConnections() {
	list, err := w.u.core.ListConnections()
	if err != nil {
		w.u.showError(err)
		return
	}
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.Name)
	}
	fyne.Do(func() {
		w.connSel.SetOptions(names)
		if w.conn != "" {
			w.connSel.SetSelected(w.conn)
			w.loadDatabases(w.conn)
		}
	})
}

func (w *exportWizard) loadDatabases(conn string) {
	go func() {
		dbs, err := w.u.core.ListDatabases(context.Background(), conn)
		if err != nil {
			fyne.Do(func() { w.u.showError(err) })
			return
		}
		names := make([]string, 0, len(dbs))
		for _, d := range dbs {
			names = append(names, d.Name)
		}
		fyne.Do(func() {
			w.dbSel.SetOptions(names)
			if w.database != "" {
				w.dbSel.SetSelected(w.database)
				w.loadObjects(conn, w.database)
			}
		})
	}()
}

func (w *exportWizard) loadObjects(conn, db string) {
	go func() {
		objs, err := w.u.core.ListObjects(context.Background(), conn, db, "")
		if err != nil {
			fyne.Do(func() { w.u.showError(err) })
			return
		}
		names := make([]string, 0, len(objs))
		for _, o := range objs {
			names = append(names, o.Name)
		}
		fyne.Do(func() {
			w.tableSel.SetOptions(names)
			if w.table != "" {
				w.tableSel.SetSelected(w.table)
			}
		})
	}()
}

// goto 渲染指定步骤，并同步步骤条高亮与页脚按钮。
func (w *exportWizard) goTo(step int) {
	w.step = step
	w.renderStepBar()
	w.body.Objects = []fyne.CanvasObject{w.stepContent(step)}
	w.body.Refresh()
	w.renderFooter()
}

func (w *exportWizard) renderStepBar() {
	w.stepBar.Objects = w.stepBar.Objects[:0]
	for i, name := range exportStepNames {
		style := fyne.TextStyle{}
		if i == w.step {
			style.Bold = true
		}
		label := widget.NewLabelWithStyle(fmt.Sprintf("%d. %s", i+1, name), fyne.TextAlignCenter, style)
		w.stepBar.Add(label)
		if i < len(exportStepNames)-1 {
			w.stepBar.Add(widget.NewLabel("›"))
		}
	}
	w.stepBar.Refresh()
}

func (w *exportWizard) renderFooter() {
	w.footer.Objects = w.footer.Objects[:0]
	if w.step > exportStepSource {
		w.footer.Add(widget.NewButton("上一步", func() { w.goTo(w.step - 1) }))
	}
	w.footer.Add(layout.NewSpacer())
	canNext := w.canProceed(w.step)
	next := widget.NewButton("下一步", func() { w.goTo(w.step + 1) })
	if canNext {
		next.Enable()
	} else {
		next.Disable()
	}
	if w.step == exportStepRun {
		w.footer.Add(widget.NewButton("开始导出", w.runExport))
	} else {
		w.footer.Add(next)
	}
	w.footer.Refresh()
}

// canProceed 控制每一步的「下一步」是否可用：保证顺序执行。
func (w *exportWizard) canProceed(step int) bool {
	switch step {
	case exportStepSource:
		return w.conn != "" && w.database != ""
	case exportStepObject:
		return w.wholeDB || w.table != ""
	case exportStepFormat:
		return w.format == "sql" || w.format == "csv"
	case exportStepPath:
		return strings.TrimSpace(w.path) != ""
	}
	return false
}

func (w *exportWizard) stepContent(step int) fyne.CanvasObject {
	switch step {
	case exportStepSource:
		return w.sourceStep()
	case exportStepObject:
		return w.objectStep()
	case exportStepFormat:
		return w.formatStep()
	case exportStepPath:
		return w.pathStep()
	default:
		return w.runStep()
	}
}

// singleSelect 懒初始化一个下拉框，选中后回调通知向导刷新后续依赖项。
func (w *exportWizard) singleSelect(sel **widget.Select, place string, onChosen func(string)) *widget.Select {
	if *sel == nil {
		*sel = widget.NewSelect([]string{}, func(v string) { onChosen(v) })
		(*sel).PlaceHolder = place
	}
	return *sel
}

func (w *exportWizard) sourceStep() fyne.CanvasObject {
	connSel := w.singleSelect(&w.connSel, "选择连接", func(v string) {
		w.conn = v
		w.database, w.table = "", ""
		w.dbSel.SetSelected("")
		w.tableSel.SetSelected("")
		w.loadDatabases(v)
		w.renderFooter()
	})
	dbSel := w.singleSelect(&w.dbSel, "选择数据库", func(v string) {
		w.database = v
		w.table = ""
		w.tableSel.SetSelected("")
		w.loadObjects(w.conn, v)
		w.renderFooter()
	})
	return container.NewVBox(
		widget.NewLabel("第一步：选择要导出的连接与数据库。"),
		widget.NewLabel("连接"), connSel,
		widget.NewLabel("数据库"), dbSel,
	)
}

func (w *exportWizard) objectStep() fyne.CanvasObject {
	w.wholeDBCheck = widget.NewCheck("导出整个数据库（每张表一个文件）", func(checked bool) {
		w.wholeDB = checked
		if checked {
			w.tableSel.Disable()
		} else {
			w.tableSel.Enable()
		}
		w.renderFooter()
	})
	w.wholeDBCheck.Checked = w.wholeDB
	if w.wholeDB {
		w.wholeDBCheck.Disable()
	} else {
		w.wholeDBCheck.Enable()
	}
	tableSel := w.singleSelect(&w.tableSel, "选择单表", func(v string) {
		w.table = v
		w.renderFooter()
	})
	if w.wholeDB {
		tableSel.Disable()
	}
	return container.NewVBox(
		widget.NewLabel("第二步：选择整库导出，或指定单张表。"),
		w.wholeDBCheck, widget.NewLabel("表"), tableSel,
	)
}

func (w *exportWizard) formatStep() fyne.CanvasObject {
	if w.formatRad == nil {
		w.formatRad = widget.NewRadioGroup([]string{"sql", "csv"}, func(v string) {
			w.format = v
			w.perTable = w.wholeDB
			w.renderFooter()
		})
		w.formatRad.SetSelected(w.format)
	}
	perTable := widget.NewCheck("每张表单独成文件（整库导出默认开启）", func(checked bool) {
		w.perTable = checked
	})
	perTable.Checked = w.wholeDB || w.perTable
	if w.wholeDB {
		perTable.Disable()
	} else {
		perTable.Enable()
	}

	desc := widget.NewLabel("SQL：生成 INSERT 语句文件。\nCSV：生成逗号分隔数据文件（NULL 输出为空）。")
	desc.Wrapping = fyne.TextWrapWord
	return container.NewVBox(
		widget.NewLabel("第三步：选择导出格式。"),
		w.formatRad, perTable, widget.NewSeparator(), desc,
	)
}

func (w *exportWizard) pathStep() fyne.CanvasObject {
	if w.pathEnt == nil {
		home, _ := os.UserHomeDir()
		defaultPath := filepath.Join(home, "Downloads")
		if w.wholeDB {
			defaultPath = filepath.Join(defaultPath, safeFileName(w.database)+"_export")
		} else if w.table != "" {
			ext := ".sql"
			if w.format == "csv" {
				ext = ".csv"
			}
			defaultPath = filepath.Join(defaultPath, safeFileName(w.table)+ext)
		}
		w.pathEnt = widget.NewEntry()
		w.pathEnt.SetText(defaultPath)
		w.path = defaultPath
		w.pathEnt.OnChanged = func(s string) {
			w.path = s
			w.renderFooter()
		}
	}
	browse := widget.NewButton("浏览…", w.pickPath)

	maxEntry := widget.NewEntry()
	maxEntry.SetText(fmt.Sprintf("%d", w.maxRows))
	maxEntry.OnChanged = func(s string) {
		if n, err := fmt.Sscanf(s, "%d", &w.maxRows); err != nil || n != 1 {
			w.maxRows = 0
		}
	}

	return container.NewVBox(
		widget.NewLabel("第四步：选择保存路径。整库导出请选择目录。"),
		container.NewBorder(nil, nil, widget.NewLabel("路径"), nil, container.NewHBox(w.pathEnt, browse)),
		widget.NewLabel("最大行数（每张表，0 表示使用默认上限）"), maxEntry,
	)
}

func (w *exportWizard) pickPath() {
	if w.wholeDB {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				w.u.showError(err)
				return
			}
			if uri == nil {
				return
			}
			w.pathEnt.SetText(uri.Path())
		}, w.u.win)
		return
	}
	dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			w.u.showError(err)
			return
		}
		if writer == nil {
			return
		}
		path := writer.URI().Path()
		_ = writer.Close()
		w.pathEnt.SetText(path)
	}, w.u.win)
}

func (w *exportWizard) runStep() fyne.CanvasObject {
	if w.log == nil {
		w.log = widget.NewLabel("准备就绪。")
		w.log.Wrapping = fyne.TextWrapWord
	}
	summary := fmt.Sprintf(
		"连接：%s\n数据库：%s\n对象：%s\n格式：%s\n路径：%s\n最大行数/表：%d",
		w.conn, w.database, objectLabel(w), w.format, w.path, w.maxRows,
	)
	return container.NewVBox(
		widget.NewLabel("第五步：确认配置并执行导出。"),
		widget.NewLabel(summary),
		widget.NewSeparator(),
		w.log,
	)
}

func objectLabel(w *exportWizard) string {
	if w.wholeDB {
		mode := "单文件"
		if w.perTable {
			mode = "每表一个文件"
		}
		return "整个数据库 (" + mode + ")"
	}
	if w.table == "" {
		return "（未选择表）"
	}
	return w.table
}

// runExport 收集表清单，逐表查询并写出到 SQL / CSV；整库按表分文件存放目录。
func (w *exportWizard) runExport() {
	if w.running {
		return
	}
	w.running = true
	defer func() { w.running = false }()

	tables := []string{w.table}
	if w.wholeDB {
		objs, err := w.u.core.ListObjects(context.Background(), w.conn, w.database, "")
		if err != nil {
			w.setLog("读取表清单失败：" + err.Error())
			return
		}
		tables = tables[:0]
		for _, o := range objs {
			tables = append(tables, o.Name)
		}
	}

	if w.wholeDB {
		if err := os.MkdirAll(w.path, 0o755); err != nil {
			w.setLog("创建目录失败：" + err.Error())
			return
		}
	}

	kind := w.u.kindOfConn(w.conn)
	quoter := sqlQuoterForKind(kind)
	totalRows := int64(0)
	var written []string

	for _, t := range tables {
		ref := domain.ObjectRef{Database: w.database, Name: t}
		sink := &core.CollectSink{}
		out, err := w.u.core.Execute(context.Background(), w.conn, domain.Query{
			Ref: ref, Database: w.database, MaxRows: w.maxRows,
		}, sink)
		if err != nil {
			w.setLog(fmt.Sprintf("导出 %s 失败：%s", t, err.Error()))
			return
		}
		var fp string
		if w.wholeDB {
			ext := ".csv"
			if w.format == "sql" {
				ext = ".sql"
			}
			fp = filepath.Join(w.path, safeFileName(t)+ext)
		} else {
			fp = w.path
		}
		f, err := os.Create(fp)
		if err != nil {
			w.setLog("创建文件失败：" + err.Error())
			return
		}
		if w.format == "sql" {
			if len(out.Pages) > 0 {
				err = export.SQL(f, t, out.Pages[0], quoter)
			}
		} else if len(out.Pages) > 0 {
			err = export.CSV(f, out.Pages[0])
		}
		_ = f.Close()
		if err != nil {
			w.setLog("写入 " + fp + " 失败：" + err.Error())
			return
		}
		totalRows += out.Summary.Rows
		written = append(written, fp)
		w.setLog(fmt.Sprintf("已导出 %s：%d 行 → %s", t, out.Summary.Rows, fp))
	}

	w.setLog(fmt.Sprintf("导出完成：%d 张表 / 集合，共 %d 行。\n%s",
		len(written), totalRows, strings.Join(written, "\n")))
	w.u.setStatus("导出完成：%d 行 → %s", totalRows, w.path)
}

func (w *exportWizard) setLog(msg string) {
	if w.log == nil {
		return
	}
	fyne.Do(func() {
		w.log.SetText(msg)
	})
}

// sqlQuoterForKind 按数据库类型返回标识符引用规则，保证导出的 INSERT 跨方言可读。
func sqlQuoterForKind(kind domain.Kind) func(string) string {
	switch kind {
	case domain.KindPostgreSQL:
		return func(s string) string {
			return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
		}
	case domain.KindSQLServer:
		return func(s string) string {
			return "[" + strings.ReplaceAll(s, "]", "]]") + "]"
		}
	default:
		return func(s string) string {
			return "`" + strings.ReplaceAll(s, "`", "``") + "`"
		}
	}
}

// kindOfConn 由连接名查回其数据库类型，用于决定 SQL 导出的标识符引用风格。
func (u *ui) kindOfConn(conn string) domain.Kind {
	list, err := u.core.ListConnections()
	if err != nil {
		return ""
	}
	for _, c := range list {
		if c.Name == conn {
			return c.Kind
		}
	}
	return ""
}

// showImportFile 打开 Windows 文件选择框选择一个 SQL 文件，dry-run 预览后确认执行。
// SQL 文件自带库/表信息，因此不要求先选中具体表。
func (u *ui) showImportFile() {
	if !u.requireConnection() {
		return
	}
	conn := u.currentConn
	ref := domain.ObjectRef{Database: u.currentDB}
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
		u.setStatus("正在解析 SQL 文件 %s …", path)
		u.async(func() {
			dry, err := u.core.ImportFile(context.Background(), conn, ref, path,
				core.ImportOptions{Format: "sql", DryRun: true})
			fyne.Do(func() {
				if err != nil {
					u.showError(err)
					return
				}
				message := fmt.Sprintf("文件: %s\n格式: %s\n语句数: %d\n\n%s",
					path, dry.Format, dry.Statements, strings.Join(dry.Preview, "\n---\n"))
				dialog.NewConfirm("确认导入 SQL", message, func(ok bool) {
					if !ok {
						return
					}
					u.async(func() {
						result, err := u.core.ImportFile(context.Background(), conn, ref, path,
							core.ImportOptions{Format: "sql", DryRun: false})
						fyne.Do(func() {
							if err != nil {
								u.showError(err)
								return
							}
							u.setStatus("已执行 %d 条 SQL 语句", result.Result.Applied)
							u.showInfo("完成", fmt.Sprintf("已执行 %d 条 SQL 语句", result.Result.Applied))
						})
					})
				}, u.win).Show()
			})
		})
	}, u.win)
}
