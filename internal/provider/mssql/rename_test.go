package mssql

import (
	"context"
	"strings"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestRenameTableScript(t *testing.T) {
	ref := domain.ObjectRef{Database: "app", Schema: "dbo", Name: "users"}
	script, err := (&schemaEditor{}).AlterTableScript(context.Background(), ref,
		[]domain.SchemaChange{{Kind: domain.ChangeRenameTable, NewName: "members"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(script.Statements) != 2 ||
		script.Statements[1] != "EXEC sp_rename N'dbo.users', N'members', N'OBJECT'" {
		t.Fatalf("重命名脚本不正确: %+v", script.Statements)
	}
	if len(script.Rollback) != 1 ||
		!strings.Contains(script.Rollback[0], "N'dbo.members', N'users'") {
		t.Fatalf("回滚脚本不正确: %+v", script.Rollback)
	}
}
