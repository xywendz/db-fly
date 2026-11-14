package logging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/db-fly/db-fly/internal/platform/audit"
)

func TestSetupCreatesLogsDirAndAudit(t *testing.T) {
	dir := t.TempDir()
	lg, closeFn, err := Setup(dir)
	if err != nil {
		t.Fatalf("Setup 失败: %v", err)
	}
	defer closeFn()
	if lg == nil {
		t.Fatal("审计日志器不应为 nil")
	}
	// 应在 base/logs 下创建日志目录与审计文件。
	if _, err := os.Stat(filepath.Join(dir, "logs")); err != nil {
		t.Fatalf("logs 目录未创建: %v", err)
	}
	lg.Log(audit.Entry{Connection: "c", Action: "query", OK: true})
	if _, err := os.Stat(lg.Path()); err != nil {
		t.Fatalf("audit.log 未创建: %v", err)
	}
}
