package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/db-fly/db-fly/internal/domain"
)

// dataCmd 提供「表数据浏览」与「清空表数据」两类命令。
func (c *cli) dataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data",
		Short: "浏览表数据（支持 filter/sort）与清空表数据（truncate/empty）",
	}
	cmd.AddCommand(c.dataBrowseCmd(), c.dataTruncateCmd(), c.dataEmptyCmd())
	return cmd
}

func (c *cli) dataBrowseCmd() *cobra.Command {
	var (
		conn, database, tableName, collection, filter, sortText, format, output string
		maxRows                                                                 int
	)
	cmd := &cobra.Command{
		Use:   "browse",
		Short: "按 filter / sort 条件浏览表或集合数据",
		Long: "不写 --sql，由各数据库模块生成预览语句；\n" +
			"关系型的 --filter/--sort 是 WHERE / ORDER BY 片段（如 --filter \"id > 10\" --sort \"id DESC\"），\n" +
			"MongoDB 是 Extended JSON（如 --filter '{\"age\":{\"$gt\":18}}' --sort '{\"age\":-1}'），\n" +
			"Redis 的 --filter 是 SCAN 键匹配模式。",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "table" && format != "json" && format != "csv" {
				return fmt.Errorf("不支持的输出格式 %q（可用 table/json/csv）", format)
			}
			if tableName == "" && collection != "" {
				tableName = collection
			}
			if tableName == "" && strings.TrimSpace(filter) == "" {
				return fmt.Errorf("请指定 --table/--collection，或用 --filter 扫描全部键")
			}
			q := domain.Query{
				Ref:        domain.ObjectRef{Database: database, Name: tableName},
				Database:   database,
				Collection: collection,
				Filter:     filter,
				Sort:       sortText,
				MaxRows:    maxRows,
			}
			out, err := c.app.Execute(cmd.Context(), conn, q, nil)
			if err != nil {
				return err
			}
			return c.writeOutcomes(cmd, []domain.QueryOutcome{out}, format, output)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&database, "database", "", "数据库 / MongoDB 数据库 / Redis 逻辑库")
	cmd.Flags().StringVar(&tableName, "table", "", "表名 / MongoDB 集合名 / Redis 键名")
	cmd.Flags().StringVar(&collection, "collection", "", "MongoDB 集合名（--table 的别名）")
	cmd.Flags().StringVar(&filter, "filter", "", "过滤条件（SQL 片段 / filter JSON / 键匹配模式）")
	cmd.Flags().StringVar(&sortText, "sort", "", "排序条件（ORDER BY 片段 / sort JSON）")
	cmd.Flags().StringVar(&format, "format", "table", "输出格式: table/json/csv")
	cmd.Flags().StringVar(&output, "output", "", "输出到文件")
	cmd.Flags().IntVar(&maxRows, "max-rows", 100, "最大预览行数")
	_ = cmd.MarkFlagRequired("conn")
	return cmd
}

// tableActionCmd 生成 TRUNCATE 或 DELETE FROM 脚本，默认仅预览。
func (c *cli) tableActionCmd(use, short string, action domain.TableAction) *cobra.Command {
	var (
		conn, database, tableName string
		apply, yes                bool
	)
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref := domain.ObjectRef{Database: database, Name: tableName}
			script, err := c.app.PreviewTableAction(cmd.Context(), conn, ref, action)
			if err != nil {
				return err
			}
			return c.previewAndApply(cmd, conn, script, apply, yes)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&database, "database", "", "数据库（必填，关系型使用全限定表名）")
	cmd.Flags().StringVar(&tableName, "table", "", "表名（必填）")
	cmd.Flags().BoolVar(&apply, "apply", false, "执行脚本（默认仅预览）")
	cmd.Flags().BoolVar(&yes, "yes", false, "非交互确认")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("database")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

func (c *cli) dataTruncateCmd() *cobra.Command {
	return c.tableActionCmd("truncate",
		"生成 TRUNCATE TABLE 脚本（不可回滚，通常重置自增序列）", domain.TableActionTruncate)
}

func (c *cli) dataEmptyCmd() *cobra.Command {
	return c.tableActionCmd("empty",
		"生成 DELETE FROM 脚本（逐行删除，不重置自增序列）", domain.TableActionEmpty)
}

// scriptComment 返回脚本预览使用的注释前缀。
func scriptComment(format string) string {
	if format == "mongo_command" {
		return "//"
	}
	if strings.Contains(format, "redis") {
		return "#"
	}
	return "--"
}
