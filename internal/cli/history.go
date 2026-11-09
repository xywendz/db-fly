package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/db-fly/db-fly/internal/platform/config"
)

func (c *cli) historyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "history", Short: "查看与清理本地查询历史"}
	cmd.AddCommand(c.historyListCmd(), c.historyClearCmd())
	return cmd
}

func (c *cli) historyListCmd() *cobra.Command {
	var limit int
	var format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出最近的查询、执行与导入记录",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "table" && format != "json" {
				return fmt.Errorf("不支持的输出格式 %q（可用 table/json）", format)
			}
			entries, err := c.app.ListHistory(limit)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
			}
			fmt.Fprintln(w, "时间\t连接\t动作\t状态\t耗时\t行数\t语句")
			for _, e := range entries {
				status := "OK"
				if !e.OK {
					status = "FAIL"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%dms\t%d\t%s\n",
					e.At.Local().Format("2006-01-02 15:04:05"), e.Connection,
					e.Action, status, e.DurationMs, e.Rows, oneLine(e.Text, 60))
			}
			if len(entries) == 0 {
				fmt.Fprintln(w, "（暂无查询历史）")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "显示条数（0 表示全部）")
	cmd.Flags().StringVar(&format, "format", "table", "输出格式: table/json")
	return cmd
}

func (c *cli) historyClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "清空全部查询历史",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := c.app.ClearHistory(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "查询历史已清空")
			return nil
		},
	}
}

func (c *cli) favoriteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "favorite", Aliases: []string{"fav"}, Short: "管理收藏的查询"}
	cmd.AddCommand(
		c.favoriteListCmd(),
		c.favoriteAddCmd(),
		c.favoriteRemoveCmd(),
		c.favoriteRunCmd(),
	)
	return cmd
}

func (c *cli) favoriteListCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出全部收藏",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "table" && format != "json" {
				return fmt.Errorf("不支持的输出格式 %q（可用 table/json）", format)
			}
			list, err := c.app.ListSavedQueries()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			fmt.Fprintln(w, "名称\t连接\t数据库\t集合\t语言\t更新时间\t语句")
			for _, q := range list {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					q.Name, q.Connection, q.Database, q.Collection, q.Language,
					q.UpdatedAt.Local().Format("2006-01-02 15:04"), oneLine(q.Text, 60))
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "（暂无收藏）")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "输出格式: table/json")
	return cmd
}

func (c *cli) favoriteAddCmd() *cobra.Command {
	var (
		name, conn, sqlText, file, database, collection, language string
		useStdin                                                  bool
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "新增或更新一条收藏",
		RunE: func(cmd *cobra.Command, _ []string) error {
			text, err := readSQL(sqlText, file, useStdin)
			if err != nil {
				return err
			}
			lang, err := parseLanguage(language)
			if err != nil {
				return err
			}
			if lang == "" && conn != "" {
				if lang, err = c.defaultLanguage(conn); err != nil {
					return err
				}
			}
			saved, err := c.app.SaveSavedQuery(config.SavedQuery{
				Name: name, Connection: conn, Database: database,
				Collection: collection, Language: string(lang), Text: text,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已保存收藏 %q（ID %s）\n", saved.Name, saved.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "收藏名称（必填）")
	cmd.Flags().StringVar(&conn, "conn", "", "默认连接名称或 ID")
	cmd.Flags().StringVar(&sqlText, "sql", "", "查询文本")
	cmd.Flags().StringVar(&file, "file", "", "从文件读取查询文本")
	cmd.Flags().StringVar(&database, "database", "", "默认数据库")
	cmd.Flags().StringVar(&collection, "collection", "", "MongoDB 集合")
	cmd.Flags().StringVar(&language, "language", "", "auto/sql/mongo_filter/mongo_pipeline")
	cmd.Flags().BoolVar(&useStdin, "stdin", false, "从标准输入读取查询文本")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func (c *cli) favoriteRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <名称或ID>",
		Short: "删除一条收藏",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.app.DeleteSavedQuery(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已删除收藏 %q\n", args[0])
			return nil
		},
	}
}

func (c *cli) favoriteRunCmd() *cobra.Command {
	var (
		conn, database, collection, language, format, output string
		maxRows, timeout                                     int
		explain, allowWrite, exec                            bool
	)
	cmd := &cobra.Command{
		Use:   "run <名称或ID>",
		Short: "执行一条收藏的查询",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := c.app.ListSavedQueries()
			if err != nil {
				return err
			}
			var saved *config.SavedQuery
			for i := range list {
				if list[i].ID == args[0] || list[i].Name == args[0] {
					saved = &list[i]
					break
				}
			}
			if saved == nil {
				return fmt.Errorf("收藏 %q 不存在", args[0])
			}
			o := queryOptions{
				conn: conn, text: saved.Text, database: database, collection: collection,
				language: language, format: format, output: output,
				maxRows: maxRows, timeout: timeout,
				explain: explain, allowWrite: allowWrite, exec: exec,
			}
			if o.conn == "" {
				o.conn = saved.Connection
			}
			if o.conn == "" {
				return fmt.Errorf("收藏 %q 未绑定连接，请用 --conn 指定", saved.Name)
			}
			if o.database == "" {
				o.database = saved.Database
			}
			if o.collection == "" {
				o.collection = saved.Collection
			}
			if o.language == "" {
				o.language = saved.Language
			}
			return c.runQuery(cmd.Context(), cmd, o)
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "覆盖收藏绑定的连接")
	cmd.Flags().StringVar(&database, "database", "", "覆盖收藏绑定的数据库")
	cmd.Flags().StringVar(&collection, "collection", "", "覆盖收藏绑定的集合")
	cmd.Flags().StringVar(&language, "language", "", "覆盖收藏绑定的查询语言")
	cmd.Flags().StringVar(&format, "format", "table", "输出格式: table/json/csv")
	cmd.Flags().StringVar(&output, "output", "", "输出到文件")
	cmd.Flags().IntVar(&maxRows, "max-rows", 0, "最大预览行数")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "语句超时（秒）")
	cmd.Flags().BoolVar(&explain, "explain", false, "显示执行计划")
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "允许执行写语句与 DDL")
	cmd.Flags().BoolVar(&exec, "exec", false, "作为原生语句/数据库命令执行")
	return cmd
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}
