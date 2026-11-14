package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/config"
)

// queryOptions 是 query 与 favorite run 共用的执行参数。
type queryOptions struct {
	conn, text, database, collection, language, format, output, save string
	filter, sort                                                     string
	maxRows, timeout                                                 int
	explain, allowWrite, exec                                        bool
}

func (c *cli) queryCmd() *cobra.Command {
	var (
		conn, sqlText, file, database, collection, language, format, output, save string
		filter, sortText                                                          string
		maxRows, timeout                                                          int
		explain, allowWrite, useStdin, exec                                       bool
	)
	cmd := &cobra.Command{
		Use:   "query",
		Short: "执行 SQL / MongoDB 查询并预览结果（支持 table/json/csv）",
		Long: "默认只允许读语句；执行 INSERT/UPDATE/DELETE/DDL 需显式追加 --allow-write。\n" +
			"MongoDB 连接使用 --language mongo_filter|mongo_pipeline 与 --collection，\n" +
			"查询文本为 filter/pipeline JSON；--exec 可提交原生数据库命令（需 --allow-write）。\n" +
			"结果导出示例: dbctl query --conn dev --sql 'SELECT * FROM t' --format csv --output t.csv",
		RunE: func(cmd *cobra.Command, _ []string) error {
			text, err := readSQL(sqlText, file, useStdin)
			if err != nil {
				return err
			}
			return c.runQuery(cmd.Context(), cmd, queryOptions{
				conn: conn, text: text, database: database, collection: collection,
				language: language, format: format, output: output, save: save,
				filter: filter, sort: sortText,
				maxRows: maxRows, timeout: timeout,
				explain: explain, allowWrite: allowWrite, exec: exec,
			})
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（必填）")
	cmd.Flags().StringVar(&sqlText, "sql", "", "查询文本（SQL 或 filter/pipeline JSON）")
	cmd.Flags().StringVar(&file, "file", "", "从文件读取查询文本")
	cmd.Flags().StringVar(&database, "database", "", "当前数据库（语句将在该库的同一连接上执行）")
	cmd.Flags().StringVar(&collection, "collection", "", "MongoDB 集合名")
	cmd.Flags().StringVar(&filter, "filter", "",
		"过滤条件：关系型为 WHERE 片段，MongoDB 为 filter JSON，Redis 为键匹配模式")
	cmd.Flags().StringVar(&sortText, "sort", "",
		"排序条件：关系型为 ORDER BY 片段（如 \"id DESC\"），MongoDB 为 sort JSON")
	cmd.Flags().StringVar(&language, "language", "auto", "查询语言: auto/sql/mongo_filter/mongo_pipeline/redis/es_dsl")
	cmd.Flags().StringVar(&format, "format", "table", "输出格式: table/json/csv")
	cmd.Flags().StringVar(&output, "output", "", "输出到文件（默认标准输出）")
	cmd.Flags().StringVar(&save, "save", "", "同时把本次查询保存为指定名称的收藏")
	cmd.Flags().IntVar(&maxRows, "max-rows", 0, "最大预览行数（默认取配置，负数表示不限）")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "语句超时（秒）")
	cmd.Flags().BoolVar(&explain, "explain", false, "显示执行计划")
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "允许执行写语句、DDL 与原生命令")
	cmd.Flags().BoolVar(&exec, "exec", false, "把整段文本作为单条原生语句/数据库命令执行")
	cmd.Flags().BoolVar(&useStdin, "stdin", false, "从标准输入读取查询文本")
	_ = cmd.MarkFlagRequired("conn")
	return cmd
}

func parseLanguage(s string) (domain.QueryLanguage, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return "", nil
	case "sql":
		return domain.LangSQL, nil
	case "mongo", "filter", "mongo_filter":
		return domain.LangMongoFilter, nil
	case "pipeline", "aggregate", "mongo_pipeline":
		return domain.LangMongoPipeline, nil
	case "redis", "redis_command":
		return domain.LangRedis, nil
	case "es", "es_dsl", "dsl", "elasticsearch":
		return domain.LangESDSL, nil
	default:
		return "", fmt.Errorf("不支持的查询语言 %q（可用 auto/sql/mongo_filter/mongo_pipeline/redis/es_dsl）", s)
	}
}

// defaultLanguage 依据模块声明的能力选择默认语言，通用层不判断具体数据库类型。
func (c *cli) defaultLanguage(conn string) (domain.QueryLanguage, error) {
	conns, err := c.app.ListConnections()
	if err != nil {
		return "", err
	}
	for _, cfg := range conns {
		if cfg.Name != conn && cfg.ID != conn {
			continue
		}
		caps, err := app.KindCapabilities(cfg.Kind)
		if err != nil {
			return "", err
		}
		if caps.Has(domain.CapSQL) {
			return domain.LangSQL, nil
		}
		if caps.Has(domain.CapMongoFilter) {
			return domain.LangMongoFilter, nil
		}
		if caps.Has(domain.CapKeyValues) {
			return domain.LangRedis, nil
		}
		if caps.Has(domain.CapESDSL) {
			return domain.LangESDSL, nil
		}
	}
	return domain.LangSQL, nil
}

func (c *cli) runQuery(ctx context.Context, cmd *cobra.Command, o queryOptions) error {
	if o.format != "table" && o.format != "json" && o.format != "csv" {
		return fmt.Errorf("不支持的输出格式 %q（可用 table/json/csv）", o.format)
	}
	if strings.TrimSpace(o.text) == "" {
		return fmt.Errorf("没有可执行的查询内容")
	}
	lang, err := parseLanguage(o.language)
	if err != nil {
		return err
	}
	if lang == "" {
		if lang, err = c.defaultLanguage(o.conn); err != nil {
			return err
		}
	}
	if o.save != "" {
		if _, err := c.app.SaveSavedQuery(config.SavedQuery{
			Name: o.save, Connection: o.conn, Database: o.database,
			Collection: o.collection, Language: string(lang), Text: o.text,
		}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "已保存收藏 %q\n", o.save)
	}

	if o.exec {
		if !o.allowWrite {
			return fmt.Errorf("执行原生语句/数据库命令需要 --allow-write 显式确认")
		}
		res, err := c.app.ExecStatement(ctx, o.conn, o.text, o.database)
		if err != nil {
			return err
		}
		return c.writeOutcomes(cmd, []domain.QueryOutcome{{Summary: domain.QuerySummary{
			Affected: res.RowsAffected,
			Messages: []domain.ServerMessage{{Level: "info", Text: res.Message}},
		}}}, o.format, o.output)
	}

	if lang != domain.LangSQL {
		// filter / pipeline 是完整 JSON 文本，不能按 SQL 分号拆句。
		q := domain.Query{
			Language: lang, Text: o.text, Database: o.database, Collection: o.collection,
			Filter: o.filter, Sort: o.sort,
			MaxRows: o.maxRows, TimeoutSec: o.timeout,
			ReadOnly: !o.allowWrite,
		}
		var out domain.QueryOutcome
		if o.explain {
			out, err = c.app.Explain(ctx, o.conn, q, nil)
		} else {
			out, err = c.app.Execute(ctx, o.conn, q, nil)
		}
		if err != nil {
			return err
		}
		return c.writeOutcomes(cmd, []domain.QueryOutcome{out}, o.format, o.output)
	}

	statements := app.SplitStatements(o.text)
	if len(statements) == 0 {
		return fmt.Errorf("没有可执行的语句")
	}
	outcomes := make([]domain.QueryOutcome, 0, len(statements))
	for _, stmt := range statements {
		if app.CheckReadOnly(stmt) != nil && !o.allowWrite {
			return fmt.Errorf("检测到写语句 %q，如确认执行请追加 --allow-write", firstWords(stmt))
		}
		q := domain.Query{
			Language: domain.LangSQL, Text: stmt, Database: o.database,
			Filter: o.filter, Sort: o.sort,
			MaxRows: o.maxRows, TimeoutSec: o.timeout,
		}
		if app.CheckReadOnly(stmt) != nil {
			res, err := c.app.ExecStatement(ctx, o.conn, stmt, o.database)
			if err != nil {
				return err
			}
			outcomes = append(outcomes, domain.QueryOutcome{Summary: domain.QuerySummary{
				Affected: res.RowsAffected,
				Messages: []domain.ServerMessage{{Level: "info", Text: res.Message}},
			}})
			continue
		}
		var out domain.QueryOutcome
		if o.explain {
			out, err = c.app.Explain(ctx, o.conn, q, nil)
		} else {
			out, err = c.app.Execute(ctx, o.conn, q, nil)
		}
		if err != nil {
			return err
		}
		outcomes = append(outcomes, out)
	}
	return c.writeOutcomes(cmd, outcomes, o.format, o.output)
}

func firstWords(stmt string) string {
	const max = 40
	if len(stmt) > max {
		return stmt[:max] + "…"
	}
	return stmt
}
