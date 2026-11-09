package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dbclient/dbclient/internal/app"
	"github.com/dbclient/dbclient/internal/domain"
)

func (c *cli) schemaCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "schema", Short: "浏览与修改表结构 / 执行结构变更脚本"}
	cmd.AddCommand(
		c.schemaShowCmd(),
		c.schemaDDLCmd(),
		c.schemaAlterCmd(),
		c.schemaCreateTableCmd(),
		c.schemaDropCmd(),
		c.schemaApplyCmd(),
	)
	return cmd
}

func (c *cli) schemaShowCmd() *cobra.Command {
	var conn, database, table, format string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "显示表结构（字段、索引、外键）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			schema, err := c.app.Describe(cmd.Context(), conn, domain.ObjectRef{Database: database, Name: table})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(schema)
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintf(tw, "# %s (engine=%s charset=%s collation=%s)\n",
				schema.Ref.String(), schema.Engine, schema.Charset, schema.Collation)
			fmt.Fprintln(tw, "\n# 字段\nNAME\tTYPE\tNULL\tDEFAULT\tPRIMARY\tAUTO_INC\tCOMMENT")
			for _, col := range schema.Columns {
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%v\t%v\t%s\n",
					col.Name, col.RawType, col.Nullable, col.Default,
					col.PrimaryKey, col.AutoIncrement, col.Comment)
			}
			fmt.Fprintln(tw, "\n# 索引\nNAME\tCOLUMNS\tUNIQUE\tTYPE")
			for _, idx := range schema.Indexes {
				fmt.Fprintf(tw, "%s\t%v\t%v\t%s\n", idx.Name, idx.Columns, idx.Unique, idx.IndexType)
			}
			if len(schema.ForeignKeys) > 0 {
				fmt.Fprintln(tw, "\n# 外键\nNAME\tCOLUMNS\tREFERENCES")
				for _, fk := range schema.ForeignKeys {
					fmt.Fprintf(tw, "%s\t%v\t%s(%v)\n", fk.Name, fk.Columns, fk.RefTable, fk.RefColumns)
				}
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&database, "database", "", "数据库（必填）")
	cmd.Flags().StringVar(&table, "table", "", "表名（必填）")
	cmd.Flags().StringVar(&format, "format", "table", "输出格式: table/json")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("database")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

func (c *cli) schemaDDLCmd() *cobra.Command {
	var conn, database, table string
	cmd := &cobra.Command{
		Use:   "ddl",
		Short: "显示建表语句",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ddl, err := c.app.ShowCreate(cmd.Context(), conn, domain.ObjectRef{Database: database, Name: table})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), ddl+";")
			return nil
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&database, "database", "", "数据库（必填）")
	cmd.Flags().StringVar(&table, "table", "", "表名（必填）")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("database")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

func (c *cli) schemaAlterCmd() *cobra.Command {
	var conn, database, table, changesFile string
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "alter",
		Short: "按结构化变更集生成 ALTER TABLE 脚本（JSON 文件为 []SchemaChange）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var changes []domain.SchemaChange
			if err := loadJSON(changesFile, &changes); err != nil {
				return err
			}
			ref := domain.ObjectRef{Database: database, Name: table}
			script, err := c.app.PreviewAlterTable(cmd.Context(), conn, ref, changes)
			if err != nil {
				return err
			}
			return c.previewAndApply(cmd, conn, script, apply, yes)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&database, "database", "", "数据库（必填）")
	cmd.Flags().StringVar(&table, "table", "", "表名（必填）")
	cmd.Flags().StringVar(&changesFile, "changes", "", "变更集 JSON 文件（必填）")
	cmd.Flags().BoolVar(&apply, "apply", false, "执行脚本（默认仅预览）")
	cmd.Flags().BoolVar(&yes, "yes", false, "非交互确认")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("database")
	_ = cmd.MarkFlagRequired("table")
	_ = cmd.MarkFlagRequired("changes")
	return cmd
}

func (c *cli) schemaCreateTableCmd() *cobra.Command {
	var conn, defFile string
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "create-table",
		Short: "按 TableDefinition JSON 生成 CREATE TABLE 脚本",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var def domain.TableDefinition
			if err := loadJSON(defFile, &def); err != nil {
				return err
			}
			script, err := c.app.PreviewCreateTable(cmd.Context(), conn, def)
			if err != nil {
				return err
			}
			return c.previewAndApply(cmd, conn, script, apply, yes)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&defFile, "def", "", "表定义 JSON 文件（必填）")
	cmd.Flags().BoolVar(&apply, "apply", false, "执行脚本（默认仅预览）")
	cmd.Flags().BoolVar(&yes, "yes", false, "非交互确认")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("def")
	return cmd
}

func (c *cli) schemaDropCmd() *cobra.Command {
	var conn, database, table string
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "drop",
		Short: "生成 DROP TABLE 脚本",
		RunE: func(cmd *cobra.Command, _ []string) error {
			script, err := c.app.PreviewDropObject(cmd.Context(), conn, domain.ObjectRef{Database: database, Name: table})
			if err != nil {
				return err
			}
			return c.previewAndApply(cmd, conn, script, apply, yes)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&database, "database", "", "数据库（必填）")
	cmd.Flags().StringVar(&table, "table", "", "表名（必填）")
	cmd.Flags().BoolVar(&apply, "apply", false, "执行脚本（默认仅预览）")
	cmd.Flags().BoolVar(&yes, "yes", false, "非交互确认")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("database")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

// splitMongoCommands 解析 JSON 命令数组、NDJSON 或单个可多行的命令对象。
func splitMongoCommands(text string) ([]string, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	var out []string
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("MongoDB 命令脚本不是合法 JSON: %w", err)
		}
		item := strings.TrimSpace(string(raw))
		if item == "" {
			continue
		}
		switch item[0] {
		case '[':
			var list []json.RawMessage
			if err := json.Unmarshal([]byte(item), &list); err != nil {
				return nil, fmt.Errorf("MongoDB 命令数组解析失败: %w", err)
			}
			for _, entry := range list {
				out = append(out, strings.TrimSpace(string(entry)))
			}
		case '{':
			out = append(out, item)
		default:
			return nil, fmt.Errorf("MongoDB 命令必须是 JSON 对象，收到 %q", item)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("MongoDB 命令脚本为空")
	}
	return out, nil
}

func (c *cli) schemaApplyCmd() *cobra.Command {
	var conn, file, format string
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "预览并执行自定义 SQL / MongoDB 命令脚本",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			text := string(b)
			if format == "" || format == "auto" {
				trimmed := strings.TrimSpace(text)
				if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
					format = "mongo_command"
				} else {
					format = "sql"
				}
			}
			script := domain.Script{Format: format}
			switch format {
			case "mongo_command":
				script.Statements, err = splitMongoCommands(text)
				if err != nil {
					return err
				}
				script.Warnings = []string{
					"MongoDB 命令按数组顺序执行，失败即停止；删除类操作不可恢复。",
				}
			case "sql":
				script.Statements = app.SplitStatements(text)
				script.Warnings = []string{
					"自定义脚本请使用全限定表名，或在连接配置中设置默认数据库。",
					"脚本逐条执行，MySQL DDL 隐式提交，中途失败不会自动回滚已执行语句。",
				}
			default:
				return fmt.Errorf("不支持的脚本格式 %q（可用 auto/sql/mongo_command）", format)
			}
			return c.previewAndApply(cmd, conn, script, apply, yes)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&file, "file", "", "SQL 或 MongoDB 命令脚本文件（必填）")
	cmd.Flags().StringVar(&format, "format", "auto", "脚本格式: auto/sql/mongo_command")
	cmd.Flags().BoolVar(&apply, "apply", false, "执行脚本（默认仅预览）")
	cmd.Flags().BoolVar(&yes, "yes", false, "非交互确认")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func (c *cli) databaseCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "创建、修改与浏览数据库"}
	cmd.AddCommand(c.dbListCmd(), c.dbCreateCmd(), c.dbAlterCmd())
	return cmd
}

func (c *cli) dbListCmd() *cobra.Command {
	var conn string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出数据库",
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := c.app.ListDatabases(cmd.Context(), conn)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tCHARSET\tCOLLATION\tSYSTEM")
			for _, d := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%v\n", d.Name, d.Charset, d.Collation, d.System)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().BoolVar(&asJSON, "json", false, "以 JSON 输出")
	_ = cmd.MarkFlagRequired("conn")
	return cmd
}

func (c *cli) dbCreateCmd() *cobra.Command {
	var conn, name, charset, collation string
	var ifNotExists, apply, yes bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "生成 CREATE DATABASE 脚本",
		RunE: func(cmd *cobra.Command, _ []string) error {
			script, err := c.app.PreviewCreateDatabase(cmd.Context(), conn,
				domain.DatabaseSpec{Name: name, Charset: charset, Collation: collation, IfNotExists: ifNotExists})
			if err != nil {
				return err
			}
			return c.previewAndApply(cmd, conn, script, apply, yes)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&name, "name", "", "数据库名（必填）")
	cmd.Flags().StringVar(&charset, "charset", "utf8mb4", "字符集")
	cmd.Flags().StringVar(&collation, "collation", "", "排序规则")
	cmd.Flags().BoolVar(&ifNotExists, "if-not-exists", true, "使用 IF NOT EXISTS")
	cmd.Flags().BoolVar(&apply, "apply", false, "执行脚本（默认仅预览）")
	cmd.Flags().BoolVar(&yes, "yes", false, "非交互确认")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func (c *cli) dbAlterCmd() *cobra.Command {
	var conn, name, charset, collation string
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "alter",
		Short: "生成 ALTER DATABASE 脚本",
		RunE: func(cmd *cobra.Command, _ []string) error {
			script, err := c.app.PreviewAlterDatabase(cmd.Context(), conn, name,
				domain.DatabaseSpec{Name: name, Charset: charset, Collation: collation})
			if err != nil {
				return err
			}
			return c.previewAndApply(cmd, conn, script, apply, yes)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&name, "name", "", "数据库名（必填）")
	cmd.Flags().StringVar(&charset, "charset", "", "新字符集")
	cmd.Flags().StringVar(&collation, "collation", "", "新排序规则")
	cmd.Flags().BoolVar(&apply, "apply", false, "执行脚本（默认仅预览）")
	cmd.Flags().BoolVar(&yes, "yes", false, "非交互确认")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
