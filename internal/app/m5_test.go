package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/config"
)

// 通过嵌入 domain 接口，只实现被测用例真正调用的方法。
type testDataEditor struct {
	domain.DataEditor
	applied []domain.EditRequest
}

func (e *testDataEditor) Preview(context.Context, domain.EditRequest) ([]string, error) {
	return []string{"UPDATE `db`.`t` SET `a` = 'x' WHERE `id` = '1';"}, nil
}

func (e *testDataEditor) Apply(_ context.Context, req domain.EditRequest) (domain.EditResult, error) {
	e.applied = append(e.applied, req)
	return domain.EditResult{
		Applied:    len(req.Edits),
		Statements: []string{"UPDATE `db`.`t` SET `a` = 'x' WHERE `id` = '1';"},
	}, nil
}

type testMetadata struct {
	domain.MetadataBrowser
	schema domain.TableSchema
	err    error
}

func (m *testMetadata) Describe(context.Context, domain.ObjectRef) (domain.TableSchema, error) {
	return m.schema, m.err
}

type testSession struct {
	domain.Session
	caps domain.CapabilitySet
	data domain.DataEditor
	meta domain.MetadataBrowser
}

func (s *testSession) Capabilities() domain.CapabilitySet { return s.caps }
func (s *testSession) Data() domain.DataEditor            { return s.data }
func (s *testSession) Metadata() domain.MetadataBrowser   { return s.meta }

func editCaps() domain.CapabilitySet {
	return domain.CapabilitySet{
		domain.CapSQL: true, domain.CapExplain: true, domain.CapDataEdit: true,
	}
}

func newM5App(t *testing.T) (*App, domain.ConnectionConfig) {
	t.Helper()
	store, err := config.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conn := domain.ConnectionConfig{
		ID: "conn-test", Name: "test", Kind: domain.KindMySQL,
		Host: "127.0.0.1", Port: 3306, Database: "db", TimeoutSec: 5,
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Connections = append(cfg.Connections, conn)
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return New(store, nil, nil), conn
}

func strPtr(s string) *string { return &s }

func sampleEditRequest() domain.EditRequest {
	return domain.EditRequest{
		Ref: domain.ObjectRef{Database: "db", Name: "t"},
		Edits: []domain.RowEdit{{
			Kind:   domain.EditUpdate,
			Key:    map[string]*string{"id": strPtr("1")},
			Values: map[string]*string{"a": strPtr("x")},
		}},
	}
}

func TestDataEditUseCases(t *testing.T) {
	ctx := context.Background()
	a, conn := newM5App(t)
	editor := &testDataEditor{}
	a.sessions[conn.ID] = &testSession{caps: editCaps(), data: editor, meta: &testMetadata{}}
	req := sampleEditRequest()

	previews, err := a.PreviewDataEdit(ctx, conn.Name, req)
	if err != nil || len(previews) != 1 {
		t.Fatalf("预览失败: %v / %#v", err, previews)
	}
	if len(editor.applied) != 0 {
		t.Fatal("预览不应写入数据")
	}

	result, err := a.ApplyDataEdit(ctx, conn.Name, req)
	if err != nil || result.Applied != 1 {
		t.Fatalf("回写失败: %v / %+v", err, result)
	}
	if len(editor.applied) != 1 || editor.applied[0].Ref.Name != "t" {
		t.Fatalf("请求未正确透传到模块: %+v", editor.applied)
	}

	if _, err := a.PreviewDataEdit(ctx, conn.Name, domain.EditRequest{}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("空请求应被校验拦截: %v", err)
	}

	cfg, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Connections[0].ReadOnly = true
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyDataEdit(ctx, conn.Name, req); !domain.IsCode(err, domain.CodeReadOnly) {
		t.Fatalf("只读连接应拒绝回写: %v", err)
	}

	a.sessions[conn.ID] = &testSession{
		caps: domain.CapabilitySet{}, data: editor, meta: &testMetadata{},
	}
	if _, err := a.PreviewDataEdit(ctx, conn.Name, req); !domain.IsCode(err, domain.CodeUnsupported) {
		t.Fatalf("未声明能力的模块应拒绝编辑: %v", err)
	}
}

func TestTableKeyColumns(t *testing.T) {
	ctx := context.Background()
	a, conn := newM5App(t)
	ref := domain.ObjectRef{Database: "db", Name: "t"}
	meta := &testMetadata{schema: domain.TableSchema{
		Columns: []domain.ColumnInfo{{Name: "id", PrimaryKey: true}, {Name: "name"}},
	}}
	a.sessions[conn.ID] = &testSession{caps: editCaps(), data: &testDataEditor{}, meta: meta}

	keys, err := a.TableKeyColumns(ctx, conn.Name, ref)
	if err != nil || len(keys) != 1 || keys[0] != "id" {
		t.Fatalf("主键推断错误: %v / %#v", err, keys)
	}

	meta.schema = domain.TableSchema{
		Columns: []domain.ColumnInfo{{Name: "email"}},
		Indexes: []domain.IndexInfo{{Name: "uq_email", Unique: true, Columns: []string{"email"}}},
	}
	keys, err = a.TableKeyColumns(ctx, conn.Name, ref)
	if err != nil || len(keys) != 1 || keys[0] != "email" {
		t.Fatalf("唯一索引回退错误: %v / %#v", err, keys)
	}

	meta.err = fmt.Errorf("metadata unavailable")
	a.sessions[conn.ID] = &testSession{
		caps: domain.CapabilitySet{domain.CapJSONTree: true},
		data: &testDataEditor{}, meta: meta,
	}
	keys, err = a.TableKeyColumns(ctx, conn.Name, ref)
	if err != nil || len(keys) != 1 || keys[0] != "_id" {
		t.Fatalf("文档型 _id 回退错误: %v / %#v", err, keys)
	}

	a.sessions[conn.ID] = &testSession{
		caps: domain.CapabilitySet{}, data: &testDataEditor{}, meta: meta,
	}
	if _, err := a.TableKeyColumns(ctx, conn.Name, ref); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("无键列时应要求显式指定: %v", err)
	}
}

func TestImportFormatDetectionAndParsers(t *testing.T) {
	cases := map[string]string{
		"a.sql": "sql", "b.csv": "csv", "c.tsv": "tsv",
		"d.xlsx": "xlsx", "e.json": "json", "f.ndjson": "json",
	}
	for path, want := range cases {
		got, err := detectImportFormat(path, "")
		if err != nil || got != want {
			t.Errorf("%s 推断为 %q (err=%v), 期望 %q", path, got, err, want)
		}
	}
	if _, err := detectImportFormat("x.bin", ""); err == nil {
		t.Error("未知扩展名应报错")
	}
	if _, err := detectImportFormat("x.bin", "yaml"); err == nil {
		t.Error("非法格式应报错")
	}
	if got, err := detectImportFormat("x.bin", "csv"); err != nil || got != "csv" {
		t.Errorf("显式格式未生效: %q / %v", got, err)
	}

	cols, err := normalizeColumns([]string{"\ufeffid", " name "})
	if err != nil || len(cols) != 2 || cols[0] != "id" || cols[1] != "name" {
		t.Fatalf("列名归一化错误: %#v / %v", cols, err)
	}
	if _, err := normalizeColumns([]string{"id", "id"}); err == nil {
		t.Error("重复列名应报错")
	}

	dir := t.TempDir()
	csvPath := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(csvPath, []byte("id,name\n1,alice\n2,\"bo,b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := readDelimited(csvPath, "csv")
	if err != nil || len(records) != 3 || records[2][1] != "bo,b" {
		t.Fatalf("CSV 解析错误: %#v / %v", records, err)
	}

	jsonPath := filepath.Join(dir, "data.json")
	if err := os.WriteFile(jsonPath,
		[]byte(`[{"id":1,"name":"a","extra":null},{"id":2,"active":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	columns, rows, err := readJSONRecords(jsonPath)
	if err != nil || len(rows) != 2 || len(columns) != 4 {
		t.Fatalf("JSON 数组解析错误: %#v / %d / %v", columns, len(rows), err)
	}
	if rows[0]["id"] == nil || *rows[0]["id"] != "1" || rows[0]["extra"] != nil {
		t.Errorf("JSON 单元格转换错误: %#v", rows[0])
	}

	ndjsonPath := filepath.Join(dir, "data.ndjson")
	if err := os.WriteFile(ndjsonPath, []byte("{\"a\":1}\n\n{\"a\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, rows, err = readJSONRecords(ndjsonPath)
	if err != nil || len(rows) != 2 {
		t.Fatalf("NDJSON 解析错误: %d 行 / %v", len(rows), err)
	}
}

func TestImportFileDryRunAndApply(t *testing.T) {
	ctx := context.Background()
	a, conn := newM5App(t)
	editor := &testDataEditor{}
	a.sessions[conn.ID] = &testSession{caps: editCaps(), data: editor, meta: &testMetadata{}}
	ref := domain.ObjectRef{Database: "db", Name: "t"}
	dir := t.TempDir()

	csvPath := filepath.Join(dir, "rows.csv")
	if err := os.WriteFile(csvPath, []byte("id,name\n1,alice\n2,bob\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dry, err := a.ImportFile(ctx, conn.Name, ref, csvPath,
		ImportOptions{HasHeader: true, DryRun: true, BatchSize: 1})
	if err != nil || dry.Rows != 2 || len(dry.Preview) == 0 {
		t.Fatalf("导入预览失败: %+v / %v", dry, err)
	}
	if len(editor.applied) != 0 {
		t.Fatal("dry-run 不应写入数据")
	}

	real, err := a.ImportFile(ctx, conn.Name, ref, csvPath,
		ImportOptions{HasHeader: true, BatchSize: 1, Transaction: true})
	if err != nil || real.Result.Applied != 2 {
		t.Fatalf("导入回写失败: %+v / %v", real, err)
	}
	if len(editor.applied) != 2 || !editor.applied[0].Transaction {
		t.Fatalf("分批与事务参数未透传: %+v", editor.applied)
	}
	for _, req := range editor.applied {
		if len(req.Edits) != 1 || req.Edits[0].Kind != domain.EditInsert {
			t.Fatalf("导入应生成参数化插入: %+v", req)
		}
	}
	history, err := a.ListHistory(0)
	if err != nil || len(history) != 1 || history[0].Action != "import" {
		t.Fatalf("导入未写入历史: %+v / %v", history, err)
	}

	sqlPath := filepath.Join(dir, "script.sql")
	if err := os.WriteFile(sqlPath,
		[]byte("INSERT INTO t VALUES (1);\n-- 注释\nINSERT INTO t VALUES (2);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sqlDry, err := a.ImportFile(ctx, conn.Name, ref, sqlPath, ImportOptions{DryRun: true})
	if err != nil || sqlDry.Format != "sql" || sqlDry.Statements != 2 || len(sqlDry.Preview) != 2 {
		t.Fatalf("SQL 导入预览失败: %+v / %v", sqlDry, err)
	}
}

func TestHistoryFavoritesAndPreferences(t *testing.T) {
	a, _ := newM5App(t)
	saved, err := a.SaveSavedQuery(config.SavedQuery{Name: "daily", Text: "SELECT 1", Connection: "test"})
	if err != nil || saved.ID == "" || saved.Language != string(domain.LangSQL) {
		t.Fatalf("保存收藏失败: %+v / %v", saved, err)
	}
	if _, err := a.SaveSavedQuery(config.SavedQuery{Name: "daily", Text: "SELECT 2"}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("同名收藏应被拒绝: %v", err)
	}
	if _, err := a.SaveSavedQuery(config.SavedQuery{ID: saved.ID, Name: "renamed", Text: "SELECT 3"}); err != nil {
		t.Fatal(err)
	}
	list, err := a.ListSavedQueries()
	if err != nil || len(list) != 1 || list[0].Name != "renamed" {
		t.Fatalf("收藏更新失败: %+v / %v", list, err)
	}
	if err := a.DeleteSavedQuery(saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteSavedQuery("missing"); !domain.IsCode(err, domain.CodeNotFound) {
		t.Fatalf("删除不存在的收藏应报 NotFound: %v", err)
	}

	for i := 0; i < 5; i++ {
		a.recordHistory(config.QueryHistoryEntry{
			Action: "query", Text: fmt.Sprintf("SELECT %d", i), OK: true,
		})
	}
	history, err := a.ListHistory(3)
	if err != nil || len(history) != 3 || history[0].Text != "SELECT 4" {
		t.Fatalf("历史顺序/上限错误: %+v / %v", history, err)
	}
	if err := a.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	history, _ = a.ListHistory(0)
	if len(history) != 0 {
		t.Fatal("历史未被清空")
	}

	prefs, err := a.Preferences()
	if err != nil {
		t.Fatal(err)
	}
	prefs.MaxRows = 42
	if err := a.SavePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	prefs, err = a.Preferences()
	if err != nil || prefs.MaxRows != 42 {
		t.Fatalf("偏好未持久化: %+v / %v", prefs, err)
	}
}

// docResultSet 给既有 fakeResultSet 补上文档型结果集的 Document 行为。
type docResultSet struct {
	*fakeResultSet
	docs []string
}

func (r *docResultSet) Document() string {
	index := r.fakeResultSet.row - 1
	if index >= 0 && index < len(r.docs) {
		return r.docs[index]
	}
	return ""
}

func TestConsumeResultCollectsJSONDocuments(t *testing.T) {
	rs := &docResultSet{
		fakeResultSet: &fakeResultSet{pages: []domain.ResultPage{makePage(2, 3)}},
		docs:          []string{`{"a":1}`, `{"a":2}`, `{"a":3}`},
	}
	out, err := consumeResult(&CollectSink{}, rs, nil, domain.Query{MaxRows: 0}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 1 || len(out.Pages[0].JSONDocuments) != 3 {
		t.Fatalf("原始 JSON 文档未透出: %+v", out.Pages)
	}
	if out.Pages[0].JSONDocuments[2] != `{"a":3}` {
		t.Error("文档顺序不正确")
	}
}
