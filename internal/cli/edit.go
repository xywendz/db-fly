package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/db-fly/db-fly/internal/domain"
)

func (c *cli) editCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "结果集数据编辑回写（结构化变更 → 参数化脚本）",
		Long: "编辑请求为 JSON：{\"ref\":{\"database\":\"db\",\"name\":\"t\"}," +
			"\"key_columns\":[\"id\"],\"transaction\":true," +
			"\"edits\":[{\"kind\":\"update\",\"key\":{\"id\":\"1\"},\"values\":{\"name\":\"x\"}}]}\n" +
			"values 中的 null 表示 SQL NULL / BSON null；kind 可用 update/delete/insert。",
	}
	cmd.AddCommand(c.editFileCmd(false), c.editFileCmd(true), c.editKeysCmd())
	return cmd
}

func (c *cli) editFileCmd(apply bool) *cobra.Command {
	var (
		conn, file, database, schema, table, keys string
		transaction, dryRun, yes                  bool
	)
	use, short := "preview", "预览生成的参数化语句/命令"
	if apply {
		use, short = "apply", "执行编辑回写（需预览确认 --yes）"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var req domain.EditRequest
			if err := loadJSON(file, &req); err != nil {
				return err
			}
			if database != "" {
				req.Ref.Database = database
			}
			if schema != "" {
				req.Ref.Schema = schema
			}
			if table != "" {
				req.Ref.Name = table
			}
			if transaction {
				req.Transaction = true
			}
			if keyColumns := splitKeys(keys); len(keyColumns) > 0 {
				req.KeyColumns = keyColumns
				fillKeysFromValues(&req)
			}
			if !apply || dryRun {
				previews, err := c.app.PreviewDataEdit(cmd.Context(), conn, req)
				if err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				for i, stmt := range previews {
					fmt.Fprintf(w, "--- 语句 %d ---\n%s\n", i+1, stmt)
				}
				fmt.Fprintln(w, "预览模式，未写入任何数据")
				return nil
			}
			if !yes {
				return fmt.Errorf("执行数据回写需要 --yes 显式确认（请先用 edit preview 复核脚本）")
			}
			res, err := c.app.ApplyDataEdit(cmd.Context(), conn, req)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "已应用 %d 条变更\n", res.Applied)
			for i, stmt := range res.Statements {
				fmt.Fprintf(w, "  [%d] %s\n", i+1, firstWords(stmt))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&file, "file", "", "编辑请求 JSON 文件（必填）")
	cmd.Flags().StringVar(&database, "database", "", "覆盖目标数据库")
	cmd.Flags().StringVar(&schema, "schema", "", "覆盖目标 schema")
	cmd.Flags().StringVar(&table, "table", "", "覆盖目标表/集合")
	cmd.Flags().StringVar(&keys, "keys", "", "逗号分隔的键列；未单独提供 key 时从 values 中提取")
	cmd.Flags().BoolVar(&transaction, "transaction", false, "使用事务（模块支持时生效）")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只预览，不写入")
	cmd.Flags().BoolVar(&yes, "yes", false, "显式确认写入")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func (c *cli) editKeysCmd() *cobra.Command {
	var conn, database, schema, table string
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "显示可安全定位一行数据的主键/唯一键列",
		RunE: func(cmd *cobra.Command, _ []string) error {
			keys, err := c.app.TableKeyColumns(cmd.Context(), conn, domain.ObjectRef{
				Database: database, Schema: schema, Name: table,
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(keys, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&database, "database", "", "数据库")
	cmd.Flags().StringVar(&schema, "schema", "", "schema（SQL Server）")
	cmd.Flags().StringVar(&table, "table", "", "表名（必填）")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

func splitKeys(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if name := strings.TrimSpace(p); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// fillKeysFromValues 允许只提交完整行 values + --keys，由 CLI 提取原始主键值。
func fillKeysFromValues(req *domain.EditRequest) {
	for i := range req.Edits {
		edit := &req.Edits[i]
		if edit.Kind == domain.EditInsert || len(edit.Key) > 0 {
			continue
		}
		key := make(map[string]*string, len(req.KeyColumns))
		for _, column := range req.KeyColumns {
			value, ok := edit.Values[column]
			if !ok {
				return
			}
			key[column] = value
		}
		edit.Key = key
	}
}
