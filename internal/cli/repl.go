package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
)

func (c *cli) replCmd() *cobra.Command {
	var conn, database, language, collection string
	var maxRows int
	cmd := &cobra.Command{
		Use:   "repl",
		Short: "交互式 SQL / MongoDB / Redis / Elasticsearch 终端",
		RunE: func(cmd *cobra.Command, _ []string) error {
			lang, err := parseLanguage(language)
			if err != nil {
				return err
			}
			if lang == "" {
				lang = domain.LangSQL
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "dbctl REPL %s\n", Version)
			fmt.Fprintln(out, "SQL 以分号结尾执行；MongoDB / Elasticsearch JSON 以空行结尾执行；Redis 命令回车即执行。")
			fmt.Fprintln(out, `\conn <name> 切换连接，\db <name> 切换数据库，`+
				`\lang <sql|filter|pipeline|redis|es_dsl> 切换语言，\collection <name> 指定集合，\q 退出。`)
			if conn == "" {
				list, err := c.app.ListConnections()
				if err != nil {
					return err
				}
				if len(list) == 0 {
					return fmt.Errorf("尚无连接配置，请先执行 dbctl conn add")
				}
				conn = list[0].Name
				fmt.Fprintf(out, "当前连接: %s\n", conn)
			}

			scanner := bufio.NewScanner(os.Stdin)
			scanner.Buffer(make([]byte, 4096), 1024*1024)
			var buf strings.Builder
			for {
				if buf.Len() > 0 {
					fmt.Fprint(out, "   -> ")
				} else if lang == domain.LangSQL {
					fmt.Fprint(out, "sql> ")
				} else if lang == domain.LangRedis {
					fmt.Fprint(out, "redis> ")
				} else if lang == domain.LangESDSL {
					fmt.Fprint(out, "es> ")
				} else {
					fmt.Fprint(out, "mongo> ")
				}
				if !scanner.Scan() {
					break
				}
				line := strings.TrimSpace(scanner.Text())
				switch {
				case line == "":
					if buf.Len() == 0 || lang == domain.LangSQL || lang == domain.LangRedis {
						continue
					}
					text := strings.TrimSpace(buf.String())
					buf.Reset()
					q := domain.Query{
						Language: lang, Text: text, Database: database,
						Collection: collection, MaxRows: maxRows,
					}
					outcome, err := c.app.Execute(cmd.Context(), conn, q, nil)
					if err != nil {
						fmt.Fprintln(out, "错误:", domain.UserMessage(err))
						continue
					}
					_ = writeTableOutcomes(out, []domain.QueryOutcome{outcome})
					continue
				case line == `\q` || line == "exit" || line == "quit":
					return nil
				case strings.HasPrefix(line, `\conn `):
					conn = strings.TrimSpace(strings.TrimPrefix(line, `\conn `))
					buf.Reset()
					fmt.Fprintf(out, "已切换连接: %s\n", conn)
					continue
				case strings.HasPrefix(line, `\db `):
					database = strings.TrimSpace(strings.TrimPrefix(line, `\db `))
					fmt.Fprintf(out, "已切换数据库: %s\n", database)
					continue
				case strings.HasPrefix(line, `\lang `):
					value := strings.TrimSpace(strings.TrimPrefix(line, `\lang `))
					parsed, err := parseLanguage(value)
					if err != nil {
						fmt.Fprintln(out, "错误:", err)
						continue
					}
					lang = parsed
					buf.Reset()
					fmt.Fprintf(out, "已切换查询语言: %s\n", lang)
					continue
				case strings.HasPrefix(line, `\collection `):
					collection = strings.TrimSpace(strings.TrimPrefix(line, `\collection `))
					fmt.Fprintf(out, "已切换集合: %s\n", collection)
					continue
				}
				buf.WriteString(line)
				buf.WriteByte('\n')
				if lang == domain.LangRedis {
					text := strings.TrimSpace(buf.String())
					buf.Reset()
					q := domain.Query{
						Language: lang, Text: text, Database: database, MaxRows: maxRows,
					}
					outcome, err := c.app.Execute(cmd.Context(), conn, q, nil)
					if err != nil {
						fmt.Fprintln(out, "错误:", domain.UserMessage(err))
						continue
					}
					_ = writeTableOutcomes(out, []domain.QueryOutcome{outcome})
					continue
				}
				if lang != domain.LangSQL {
					continue
				}
				if !strings.HasSuffix(line, ";") {
					continue
				}
				text := buf.String()
				buf.Reset()
				for _, stmt := range app.SplitStatements(text) {
					q := domain.Query{
						Language: domain.LangSQL, Text: stmt,
						Database: database, MaxRows: maxRows,
					}
					if app.CheckReadOnly(stmt) != nil {
						res, err := c.app.ExecStatement(cmd.Context(), conn, stmt, database)
						if err != nil {
							fmt.Fprintln(out, "错误:", domain.UserMessage(err))
							continue
						}
						fmt.Fprintln(out, res.Message)
						continue
					}
					outcome, err := c.app.Execute(cmd.Context(), conn, q, nil)
					if err != nil {
						fmt.Fprintln(out, "错误:", domain.UserMessage(err))
						continue
					}
					_ = writeTableOutcomes(out, []domain.QueryOutcome{outcome})
				}
			}
			return scanner.Err()
		},
	}
	cmd.Flags().StringVar(&conn, "conn", "", "连接名称（默认取第一个连接）")
	cmd.Flags().StringVar(&database, "database", "", "初始数据库")
	cmd.Flags().StringVar(&language, "language", "sql", "初始查询语言: sql/filter/pipeline/redis/es_dsl")
	cmd.Flags().StringVar(&collection, "collection", "", "MongoDB 集合")
	cmd.Flags().IntVar(&maxRows, "max-rows", 100, "REPL 中单条查询最大显示行数")
	return cmd
}
