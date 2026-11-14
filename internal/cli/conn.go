package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/db-fly/db-fly/internal/domain"
)

func (c *cli) connCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "conn", Short: "管理连接配置"}
	cmd.AddCommand(c.connListCmd(), c.connAddCmd(), c.connTestCmd(), c.connRemoveCmd())
	return cmd
}

func (c *cli) connListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出全部连接",
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := c.app.ListConnections()
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
			fmt.Fprintln(tw, "NAME\tKIND\tHOST\tPORT\tUSER\tDATABASE\tREADONLY")
			for _, x := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%v\n",
					x.Name, x.Kind, x.Host, x.Port, x.User, x.Database, x.ReadOnly)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "以 JSON 输出")
	return cmd
}

func (c *cli) connAddCmd() *cobra.Command {
	var (
		name, kind, host, user, database, tlsMode, password, paramsText string
		port, timeout                                                   int
		readOnly, passwordStdin                                         bool
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "新增或更新连接（同名即更新）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw, err := readPassword(password, passwordStdin)
			if err != nil {
				return err
			}
			selectedKind := normalizeKind(kind)
			if !cmd.Flags().Changed("port") {
				port = domain.DefaultPort(selectedKind)
			}
			params, err := parseParams(paramsText)
			if err != nil {
				return err
			}
			cfg := domain.ConnectionConfig{
				Name: name, Kind: selectedKind, Host: host, Port: port,
				User: user, Database: database, TLSMode: tlsMode, Params: params,
				ReadOnly: readOnly, TimeoutSec: timeout,
			}
			if list, lerr := c.app.ListConnections(); lerr == nil {
				for _, existing := range list {
					if existing.Name == name {
						cfg.ID = existing.ID
						cfg.Group = existing.Group
						cfg.Color = existing.Color
						break
					}
				}
			}
			saved, err := c.app.SaveConnection(cfg, pw, passwordStdin || password != "")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已保存连接 %s (%s)，id=%s\n", saved.Name, saved.Kind, saved.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "连接名称（必填）")
	cmd.Flags().StringVar(&kind, "kind", string(domain.KindMySQL),
		"数据库类型: mysql/sqlserver/mongodb/postgresql/oracle/sqlite/redis/elasticsearch")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "主机（SQLite 为数据库文件路径）")
	cmd.Flags().IntVar(&port, "port", 3306, "端口（默认按 --kind 自动选择；SQLite 可为 0）")
	cmd.Flags().StringVar(&user, "user", "root", "用户名")
	cmd.Flags().StringVar(&database, "database", "", "默认数据库")
	cmd.Flags().StringVar(&tlsMode, "tls", "false", "TLS 模式: false/true/skip-verify/preferred")
	cmd.Flags().IntVar(&timeout, "timeout", 15, "连接超时（秒）")
	cmd.Flags().StringVar(&paramsText, "params", "",
		"附加参数 key=value，逗号分隔（如 oracle: service=XEPDB1；redis: username=acl；sqlite: file=./a.db）")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "只读模式")
	cmd.Flags().StringVar(&password, "password", "", "密码（明文传参，仅建议测试环境）")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "从标准输入第一行读取密码")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func (c *cli) connTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <name>",
		Short: "测试已保存连接的连通性",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := c.app.TestSavedConnection(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "连接成功：%s %s (%s)\n", info.Kind, info.Version, info.Host)
			return nil
		},
	}
}

func (c *cli) connRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "删除连接及其凭证",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.app.RemoveConnection(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已删除连接 %s\n", args[0])
			return nil
		},
	}
}

// normalizeKind 兼容常见的数据库类型别名。
func normalizeKind(s string) domain.Kind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "postgres", "pg", "pgsql":
		return domain.KindPostgreSQL
	case "sqlite3", "sqlite":
		return domain.KindSQLite
	case "mssql", "sqlserver":
		return domain.KindSQLServer
	case "mongo":
		return domain.KindMongoDB
	case "es", "elastic", "elasticsearch":
		return domain.KindElasticsearch
	default:
		return domain.Kind(strings.ToLower(strings.TrimSpace(s)))
	}
}

func parseParams(text string) (map[string]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	out := make(map[string]string)
	for _, item := range strings.Split(text, ",") {
		pair := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" {
			return nil, fmt.Errorf("附加参数 %q 不是 key=value 形式", item)
		}
		out[strings.TrimSpace(pair[0])] = strings.TrimSpace(pair[1])
	}
	return out, nil
}

func readPassword(plain string, fromStdin bool) (string, error) {
	if plain != "" {
		return plain, nil
	}
	if !fromStdin {
		return "", nil
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimRight(scanner.Text(), "\r\n"), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}
