package oracle

import (
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestRenameTableScript(t *testing.T) {
	ref := domain.ObjectRef{Database: "SCOTT", Name: "USERS"}
	statements, rollbacks, _, err := (&ddlDriver{}).AlterTable(ref,
		[]domain.SchemaChange{{Kind: domain.ChangeRenameTable, NewName: "MEMBERS"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 1 || statements[0] != `RENAME "USERS" TO "MEMBERS"` {
		t.Fatalf("重命名脚本不正确: %+v", statements)
	}
	if len(rollbacks) != 1 || rollbacks[0] != `RENAME "MEMBERS" TO "USERS"` {
		t.Fatalf("回滚脚本不正确: %+v", rollbacks)
	}
}
