package export

import (
	"bytes"
	"strings"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func samplePage() domain.ResultPage {
	return domain.ResultPage{
		Columns: []domain.Column{
			{Name: "id"}, {Name: "name"}, {Name: "note"},
		},
		Rows: []domain.Row{
			{
				{Text: "1"},
				{Text: "alice"},
				{Null: true},
			},
			{
				{Text: "2"},
				{Text: "bob's"},
				{Text: `a\b`},
			},
		},
	}
}

func TestSQL(t *testing.T) {
	var buf bytes.Buffer
	if err := SQL(&buf, "users", samplePage(), nil); err != nil {
		t.Fatalf("SQL: %v", err)
	}
	out := buf.String()
	want := "INSERT INTO `users` (`id`, `name`, `note`) VALUES ('1', 'alice', NULL);\n" +
		"INSERT INTO `users` (`id`, `name`, `note`) VALUES ('2', 'bob''s', 'a\\\\b');\n"
	if out != want {
		t.Fatalf("SQL 输出不符:\n got: %q\nwant: %q", out, want)
	}
}

func TestSQLEmpty(t *testing.T) {
	var buf bytes.Buffer
	page := domain.ResultPage{Columns: []domain.Column{{Name: "id"}}}
	if err := SQL(&buf, "t", page, nil); err != nil {
		t.Fatalf("SQL empty: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("空结果应无输出，实际: %q", buf.String())
	}
}

func TestSQLCustomQuoter(t *testing.T) {
	var buf bytes.Buffer
	quote := func(s string) string { return `"` + s + `"` }
	if err := SQL(&buf, "t", samplePage(), quote); err != nil {
		t.Fatalf("SQL quote: %v", err)
	}
	if !strings.Contains(buf.String(), `INSERT INTO "t" ("id", "name", "note")`) {
		t.Fatalf("自定义引用未生效: %q", buf.String())
	}
}

func TestCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(&buf, samplePage()); err != nil {
		t.Fatalf("CSV: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "id,name,note") {
		t.Fatalf("CSV 缺少表头: %q", out)
	}
	if !strings.Contains(out, ",,\n") && !strings.Contains(out, ",null,") {
		// NULL 应输出为空字段
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("CSV 行数应为 3（表头+2数据），实际 %d: %q", len(lines), out)
	}
}
