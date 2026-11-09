package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dbclient/dbclient/internal/app"
	"github.com/dbclient/dbclient/internal/domain"
)

func (c *cli) importCmd() *cobra.Command {
	var (
		conn, file, database, schema, table, format, sheet string
		header, transaction, dryRun, yes                   bool
		batch                                              int
	)
	cmd := &cobra.Command{
		Use:     "import",
		Aliases: []string{"load"},
		Short:   "导入 SQL 脚本或 CSV/TSV/Excel/JSON 数据",
		Long: "SQL 文件按语句顺序执行；结构化文件按列名转成参数化插入，由各数据库模块生成方言。\n" +
			"默认首行为表头（Excel 默认第一个工作表）；导入前请先用 --dry-run 预览生成语句。",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !dryRun && !yes {
				return fmt.Errorf("导入会写入数据，请先用 --dry-run 预览，确认后追加 --yes")
			}
			ref := domain.ObjectRef{Database: database, Schema: schema, Name: table}
			out, err := c.app.ImportFile(cmd.Context(), conn, ref, file, app.ImportOptions{
				Format: format, Sheet: sheet, HasHeader: header,
				BatchSize: batch, Transaction: transaction, DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "格式=%s 数据行=%d 生成语句=%d\n", out.Format, out.Rows, out.Statements)
			if dryRun {
				for i, stmt := range out.Preview {
					fmt.Fprintf(w, "--- 预览 %d ---\n%s\n", i+1, stmt)
				}
				fmt.Fprintln(w, "预览模式，未写入任何数据")
				return nil
			}
			fmt.Fprintf(w, "已写入 %d 条\n", out.Result.Applied)
			for i, stmt := range out.Result.Statements {
				fmt.Fprintf(w, "  [%d] %s\n", i+1, firstWords(stmt))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&file, "file", "", "导入文件路径（必填）")
	cmd.Flags().StringVar(&database, "database", "", "目标数据库")
	cmd.Flags().StringVar(&schema, "schema", "", "目标 schema（SQL Server）")
	cmd.Flags().StringVar(&table, "table", "", "目标表/集合（结构化数据导入必填）")
	cmd.Flags().StringVar(&format, "format", "auto", "auto/sql/csv/tsv/xlsx/json")
	cmd.Flags().StringVar(&sheet, "sheet", "", "Excel 工作表名（默认第一个）")
	cmd.Flags().BoolVar(&header, "header", true, "表格文件首行是列名")
	cmd.Flags().IntVar(&batch, "batch", 0, "每批写入条数（默认 200）")
	cmd.Flags().BoolVar(&transaction, "transaction", false, "每批使用事务（模块支持时生效）")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只预览生成的语句，不写入")
	cmd.Flags().BoolVar(&yes, "yes", false, "显式确认写入")
	_ = cmd.MarkFlagRequired("conn")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
