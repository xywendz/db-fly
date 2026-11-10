package mongo

import (
	"context"
	"strings"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestRenameCollectionScript(t *testing.T) {
	ref := domain.ObjectRef{Database: "app", Name: "users"}
	script, err := (&schemaEditor{}).AlterTableScript(context.Background(), ref,
		[]domain.SchemaChange{{Kind: domain.ChangeRenameTable, NewName: "members"}})
	if err != nil {
		t.Fatal(err)
	}
	if script.Format != "mongo_command" || len(script.Statements) != 1 ||
		!strings.Contains(script.Statements[0], `"action": "renameCollection"`) ||
		!strings.Contains(script.Statements[0], `"new_name": "members"`) {
		t.Fatalf("重命名集合命令不正确: %+v", script)
	}
	cmd, err := decodeCommand(script.Statements[0])
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Database != "app" || cmd.Collection != "users" || cmd.NewName != "members" {
		t.Fatalf("命令字段不正确: %+v", cmd)
	}
	if len(script.Rollback) != 1 || !strings.Contains(script.Rollback[0], `"collection": "members"`) {
		t.Fatalf("回滚命令不正确: %+v", script.Rollback)
	}
}
