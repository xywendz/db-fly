package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/audit"
	"github.com/db-fly/db-fly/internal/platform/config"
)

const defaultImportBatchSize = 200

// ImportOptions 控制导入解析与回写方式。DryRun 只生成预览，绝不写库。
type ImportOptions struct {
	Format      string // auto / sql / csv / tsv / xlsx / json
	Sheet       string // xlsx 工作表名，留空取第一个
	HasHeader   bool   // 表格文件首行是否为列名
	BatchSize   int
	Transaction bool
	DryRun      bool
}

// ImportOutcome 汇总一次导入。Result.Applied 为实际写入的语句/文档数。
type ImportOutcome struct {
	Format     string            `json:"format"`
	Rows       int               `json:"rows"`
	Statements int               `json:"statements"`
	Preview    []string          `json:"preview,omitempty"`
	Result     domain.EditResult `json:"result"`
}

// ImportFile 把 SQL 脚本或结构化数据文件导入指定连接。
// SQL 走逐条语句执行；CSV/TSV/XLSX/JSON 统一转成 EditInsert 结构化变更，
// 再由各数据库模块生成参数化写入语句或文档命令，避免通用层拼接方言。
func (a *App) ImportFile(ctx context.Context, conn string, ref domain.ObjectRef, path string, opts ImportOptions) (ImportOutcome, error) {
	if strings.TrimSpace(path) == "" {
		return ImportOutcome{}, domain.NewError(domain.CodeValidation, "import", "未指定导入文件")
	}
	format, err := detectImportFormat(path, opts.Format)
	if err != nil {
		return ImportOutcome{}, err
	}
	if _, err := a.cfgOf(conn); err != nil {
		return ImportOutcome{}, err
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultImportBatchSize
	}
	if format == "sql" {
		return a.importSQL(ctx, conn, ref, path, opts)
	}
	if ref.Name == "" {
		return ImportOutcome{}, domain.NewError(domain.CodeValidation, "import",
			"数据文件导入需要 --table 指定目标表/集合")
	}
	_, rows, err := a.readTabular(ctx, conn, ref, path, format, opts)
	if err != nil {
		return ImportOutcome{}, err
	}
	out := ImportOutcome{Format: format, Rows: len(rows)}
	if len(rows) == 0 {
		return out, domain.NewError(domain.CodeValidation, "import", "文件中没有可导入的数据行")
	}
	edits := make([]domain.RowEdit, 0, len(rows))
	for _, row := range rows {
		edits = append(edits, domain.RowEdit{Kind: domain.EditInsert, Values: row})
	}
	out.Statements = len(edits)

	if opts.DryRun {
		previews, err := a.PreviewDataEdit(ctx, conn, domain.EditRequest{
			Ref: ref, Edits: edits[:minInt(20, len(edits))],
		})
		if err != nil {
			return out, err
		}
		out.Preview = previews
		return out, nil
	}

	for start := 0; start < len(edits); start += opts.BatchSize {
		end := minInt(start+opts.BatchSize, len(edits))
		res, err := a.ApplyDataEdit(ctx, conn, domain.EditRequest{
			Ref: ref, Edits: edits[start:end], Transaction: opts.Transaction,
		})
		if err != nil {
			return out, fmt.Errorf("第 %d~%d 行导入失败: %w", start+1, end, err)
		}
		out.Result.Applied += res.Applied
		room := 20 - len(out.Result.Statements)
		if room > 0 {
			out.Result.Statements = append(out.Result.Statements,
				res.Statements[:minInt(room, len(res.Statements))]...)
		}
	}
	a.logImport(conn, ref, path, out)
	return out, nil
}

func (a *App) importSQL(ctx context.Context, conn string, ref domain.ObjectRef, path string, opts ImportOptions) (ImportOutcome, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ImportOutcome{}, domain.WrapError(domain.CodeValidation, "import", err, "读取 SQL 文件失败")
	}
	statements := SplitStatements(string(b))
	out := ImportOutcome{Format: "sql", Statements: len(statements)}
	if len(statements) == 0 {
		return out, domain.NewError(domain.CodeValidation, "import", "SQL 文件中没有可执行语句")
	}
	if opts.DryRun {
		out.Preview = trimStrings(statements, 20)
		return out, nil
	}
	for i, stmt := range statements {
		res, err := a.execStatement(ctx, conn, stmt, ref.Database, false)
		if err != nil {
			return out, fmt.Errorf("第 %d 条语句执行失败: %w", i+1, err)
		}
		out.Result.Applied++
		if len(out.Result.Statements) < 20 {
			out.Result.Statements = append(out.Result.Statements, res.Statement)
		}
	}
	a.logImport(conn, ref, path, out)
	return out, nil
}

func (a *App) logImport(conn string, ref domain.ObjectRef, path string, out ImportOutcome) {
	c, err := a.cfgOf(conn)
	if err != nil {
		return
	}
	a.Audit.Log(audit.Entry{
		Connection: c.Name, Kind: string(c.Kind), Action: "import",
		Statement: path + " (" + out.Format + ")", Rows: int64(out.Result.Applied), OK: true,
	})
	a.recordHistory(config.QueryHistoryEntry{
		Connection: c.Name, Kind: string(c.Kind), Action: "import",
		Database: ref.Database, Rows: int64(out.Result.Applied), OK: true,
		Text: fmt.Sprintf("导入 %s → %s（%s，%d 行）", path, ref.String(), out.Format, out.Result.Applied),
	})
}

func detectImportFormat(path, format string) (string, error) {
	if format != "" && format != "auto" {
		switch format {
		case "sql", "csv", "tsv", "xlsx", "json":
			return format, nil
		default:
			return "", domain.NewError(domain.CodeValidation, "import",
				"不支持的导入格式 %q（可用 auto/sql/csv/tsv/xlsx/json）", format)
		}
	}
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "sql":
		return "sql", nil
	case "csv":
		return "csv", nil
	case "tsv", "txt":
		return "tsv", nil
	case "xlsx", "xlsm":
		return "xlsx", nil
	case "json", "jsonl", "ndjson":
		return "json", nil
	default:
		return "", domain.NewError(domain.CodeValidation, "import",
			"无法从扩展名推断导入格式，请用 --format 显式指定")
	}
}

func (a *App) readTabular(ctx context.Context, conn string, ref domain.ObjectRef, path, format string, opts ImportOptions) ([]string, []map[string]*string, error) {
	if format == "json" {
		return readJSONRecords(path)
	}
	var records [][]string
	var err error
	switch format {
	case "csv", "tsv":
		records, err = readDelimited(path, format)
	case "xlsx":
		records, err = readXLSX(path, opts.Sheet)
	}
	if err != nil {
		return nil, nil, err
	}
	if len(records) == 0 {
		return nil, nil, domain.NewError(domain.CodeValidation, "import", "文件为空")
	}
	columns := records[0]
	start := 1
	if opts.HasHeader {
		columns, err = normalizeColumns(columns)
	} else {
		columns, err = a.tableColumns(ctx, conn, ref)
		start = 0
	}
	if err != nil {
		return nil, nil, err
	}
	rows := make([]map[string]*string, 0, len(records)-start)
	for _, record := range records[start:] {
		if isEmptyRecord(record) {
			continue
		}
		row := make(map[string]*string, len(columns))
		for i, col := range columns {
			if i >= len(record) {
				break
			}
			value := record[i]
			row[col] = &value
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	return columns, rows, nil
}

func (a *App) tableColumns(ctx context.Context, conn string, ref domain.ObjectRef) ([]string, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return nil, err
	}
	schema, err := s.Metadata().Describe(ctx, ref)
	if err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "import", err,
			"读取表结构失败，请改用带表头的文件")
	}
	cols := make([]string, 0, len(schema.Columns))
	for _, col := range schema.Columns {
		cols = append(cols, col.Name)
	}
	if len(cols) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "import",
			"目标对象没有可用列，请改用带表头的文件")
	}
	return cols, nil
}

func normalizeColumns(raw []string) ([]string, error) {
	seen := make(map[string]bool, len(raw))
	cols := make([]string, 0, len(raw))
	for i, name := range raw {
		name = strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))
		if name == "" {
			return nil, domain.NewError(domain.CodeValidation, "import", "第 %d 列列名为空", i+1)
		}
		if seen[name] {
			return nil, domain.NewError(domain.CodeValidation, "import", "列名 %q 重复", name)
		}
		seen[name] = true
		cols = append(cols, name)
	}
	if len(cols) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "import", "没有可导入的列")
	}
	return cols, nil
}

func readDelimited(path, format string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "import", err, "打开文件失败")
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if format == "tsv" {
		r.Comma = '\t'
	}
	records, err := r.ReadAll()
	if err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "import", err, "解析表格文件失败")
	}
	return records, nil
}

func readXLSX(path, sheet string) ([][]string, error) {
	book, err := excelize.OpenFile(path)
	if err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "import", err, "打开 Excel 文件失败")
	}
	defer func() { _ = book.Close() }()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "import", "工作簿中没有工作表")
	}
	if sheet == "" {
		sheet = sheets[0]
	} else {
		found := false
		for _, name := range sheets {
			if name == sheet {
				found = true
				break
			}
		}
		if !found {
			return nil, domain.NewError(domain.CodeValidation, "import",
				"工作表 %q 不存在（可用: %s）", sheet, strings.Join(sheets, ", "))
		}
	}
	records, err := book.GetRows(sheet)
	if err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "import", err, "读取工作表失败")
	}
	return records, nil
}

// readJSONRecords 接受 JSON 对象数组、单个对象或 NDJSON（每行一个对象）。
func readJSONRecords(path string) ([]string, []map[string]*string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, domain.WrapError(domain.CodeValidation, "import", err, "读取 JSON 文件失败")
	}
	data := bytes.TrimSpace(b)
	if len(data) == 0 {
		return nil, nil, domain.NewError(domain.CodeValidation, "import", "JSON 文件为空")
	}
	var documents []map[string]any
	if data[0] == '[' {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&documents); err != nil {
			return nil, nil, domain.WrapError(domain.CodeValidation, "import", err, "解析 JSON 数组失败")
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 || line[0] == '#' {
				continue
			}
			dec := json.NewDecoder(bytes.NewReader(line))
			dec.UseNumber()
			var doc map[string]any
			if err := dec.Decode(&doc); err != nil {
				return nil, nil, domain.WrapError(domain.CodeValidation, "import", err,
					"解析 JSON 对象失败（第 %d 行）", len(documents)+1)
			}
			documents = append(documents, doc)
		}
		if err := scanner.Err(); err != nil {
			return nil, nil, domain.WrapError(domain.CodeValidation, "import", err, "读取 JSON 文件失败")
		}
	}
	return documentsToRows(documents)
}

func documentsToRows(documents []map[string]any) ([]string, []map[string]*string, error) {
	seen := make(map[string]bool)
	columns := make([]string, 0)
	for _, doc := range documents {
		for key := range doc {
			if !seen[key] {
				seen[key] = true
				columns = append(columns, key)
			}
		}
	}
	sort.Strings(columns)
	if len(columns) == 0 {
		return nil, nil, domain.NewError(domain.CodeValidation, "import", "JSON 文档中没有字段")
	}
	rows := make([]map[string]*string, 0, len(documents))
	for _, doc := range documents {
		row := make(map[string]*string, len(doc))
		for key, value := range doc {
			row[key] = jsonCell(value)
		}
		rows = append(rows, row)
	}
	return columns, rows, nil
}

func jsonCell(v any) *string {
	switch value := v.(type) {
	case nil:
		return nil
	case string:
		s := value
		return &s
	case json.Number:
		s := value.String()
		return &s
	case bool:
		s := strconv.FormatBool(value)
		return &s
	default:
		b, err := json.Marshal(value)
		if err != nil {
			s := fmt.Sprint(value)
			return &s
		}
		s := string(b)
		return &s
	}
}

func isEmptyRecord(record []string) bool {
	for _, cell := range record {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func trimStrings(in []string, n int) []string {
	out := make([]string, minInt(n, len(in)))
	copy(out, in[:len(out)])
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
