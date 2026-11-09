package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLIWithDir(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	c := &cli{}
	root := c.rootCmd()
	root.SetArgs(append([]string{"--data-dir", dir}, args...))
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	err := root.Execute()
	if c.app != nil {
		_ = c.app.Close()
	}
	return out.String(), err
}

func runCLI(t *testing.T, args ...string) (string, error) {
	return runCLIWithDir(t, t.TempDir(), args...)
}

func TestCommandRegistration(t *testing.T) {
	out, err := runCLI(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"history", "favorite", "import", "edit", "query", "repl"} {
		if !strings.Contains(out, name) {
			t.Errorf("命令 %s 未注册", name)
		}
	}
}

func TestFavoriteLifecycle(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLIWithDir(t, dir,
		"favorite", "add", "--name", "daily", "--conn", "dev", "--sql", "SELECT 1")
	if err != nil || !strings.Contains(out, "daily") {
		t.Fatalf("新增收藏失败: %q / %v", out, err)
	}
	out, err = runCLIWithDir(t, dir, "favorite", "list")
	if err != nil || !strings.Contains(out, "SELECT 1") || !strings.Contains(out, "sql") {
		t.Fatalf("收藏列表失败: %q / %v", out, err)
	}
	out, err = runCLIWithDir(t, dir, "favorite", "run", "daily", "--format", "json")
	if err == nil {
		t.Fatalf("不存在的连接应导致执行失败: %q", out)
	}
	out, err = runCLIWithDir(t, dir, "favorite", "remove", "daily")
	if err != nil || !strings.Contains(out, "已删除") {
		t.Fatalf("删除收藏失败: %q / %v", out, err)
	}
	out, err = runCLIWithDir(t, dir, "favorite", "list", "--format", "json")
	if err != nil || !strings.Contains(out, "[]") {
		t.Fatalf("删除后列表应为空: %q / %v", out, err)
	}
}

func TestHistoryCommands(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLIWithDir(t, dir, "history", "list")
	if err != nil || !strings.Contains(out, "暂无查询历史") {
		t.Fatalf("历史列表失败: %q / %v", out, err)
	}
	out, err = runCLIWithDir(t, dir, "history", "clear")
	if err != nil || !strings.Contains(out, "已清空") {
		t.Fatalf("清空历史失败: %q / %v", out, err)
	}
}

func TestQueryLanguageValidation(t *testing.T) {
	_, err := runCLI(t, "query", "--conn", "nope", "--sql", "SELECT 1", "--language", "bogus")
	if err == nil || !strings.Contains(err.Error(), "不支持的查询语言") {
		t.Fatalf("非法语言未被拦截: %v", err)
	}
}

func TestSplitMongoCommands(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"array", `[{"action":"createCollection","database":"db"},{"action":"dropDatabase","database":"db"}]`, 2},
		{"ndjson", "{\"action\":\"createCollection\"}\n\n{\"action\":\"dropDatabase\"}\n", 2},
		{"pretty", "{\n  \"action\": \"createCollection\",\n  \"database\": \"db\"\n}\n", 1},
	}
	for _, c := range cases {
		got, err := splitMongoCommands(c.text)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: 得到 %d 条命令 (err=%v), 期望 %d", c.name, len(got), err, c.want)
			continue
		}
		if !strings.Contains(got[0], "createCollection") {
			t.Errorf("%s: 命令内容丢失: %s", c.name, got[0])
		}
	}
	if _, err := splitMongoCommands("not json"); err == nil {
		t.Error("非法 JSON 应报错")
	}
	if _, err := splitMongoCommands(""); err == nil {
		t.Error("空脚本应报错")
	}
}

func TestSchemaApplyMongoCommandPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	content := `[{"action":"createCollection","database":"db","collection":"c"}]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "schema", "apply", "--conn", "test", "--file", path)
	if err != nil || !strings.Contains(out, "命令 1") || !strings.Contains(out, "createCollection") {
		t.Fatalf("Mongo 命令脚本预览失败: %q / %v", out, err)
	}
	if !strings.Contains(out, "预览模式") {
		t.Errorf("默认应为预览模式: %q", out)
	}
}

func TestEditPreviewValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edit.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runCLI(t, "edit", "preview", "--conn", "test", "--file", path)
	if err == nil || !strings.Contains(err.Error(), "未指定目标表") {
		t.Fatalf("空编辑请求未被拦截: %v", err)
	}
}
