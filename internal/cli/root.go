// Package cli 实现 Linux 命令行工具 dbctl，与 GUI 共用 app 业务内核。
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dbclient/dbclient/internal/app"
	"github.com/dbclient/dbclient/internal/platform/audit"
	"github.com/dbclient/dbclient/internal/platform/config"
	"github.com/dbclient/dbclient/internal/platform/credential"
	"github.com/dbclient/dbclient/internal/provider"
	_ "github.com/dbclient/dbclient/internal/provider/all"
)

// Version 由构建脚本通过 -ldflags 注入。
var Version = "dev"

type cli struct {
	app     *app.App
	dataDir string
}

// Execute 是 dbctl 的入口。
func Execute() error {
	c := &cli{}
	err := c.rootCmd().Execute()
	if c.app != nil {
		_ = c.app.Close()
	}
	return err
}

func (c *cli) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dbctl",
		Short:         "DBClient 命令行数据库工具",
		Long:          "dbctl 是 DBClient 的命令行形态，与 Windows GUI 共用同一套业务内核与连接配置。",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&c.dataDir, "data-dir", "", "数据目录（默认 ~/.dbclient）")
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error { return c.init() }
	root.AddCommand(
		c.connCmd(),
		c.queryCmd(),
		c.schemaCmd(),
		c.databaseCmd(),
		c.replCmd(),
		c.infoCmd(),
		c.historyCmd(),
		c.favoriteCmd(),
		c.importCmd(),
		c.editCmd(),
	)
	return root
}

func (c *cli) init() error {
	dir := c.dataDir
	if dir == "" {
		var err error
		if dir, err = config.DefaultDir(); err != nil {
			return err
		}
	}
	cfg, err := config.New(dir)
	if err != nil {
		return err
	}
	secrets, err := credential.New(dir)
	if err != nil {
		return err
	}
	var logger audit.Logger = audit.Nop{}
	if l, lerr := audit.New(dir); lerr == nil {
		logger = l
	}
	c.app = app.New(cfg, secrets, logger)

	if secrets.Name() == "aes-gcm" && os.Getenv("DBCLIENT_MASTER_KEY") == "" {
		fmt.Fprintln(os.Stderr,
			"警告：未设置 DBCLIENT_MASTER_KEY，凭证使用机器本地派生密钥加密，建议显式设置以提高安全性。")
	}
	return nil
}

func (c *cli) infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "显示版本与运行环境",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "dbctl %s\n", Version)
			fmt.Fprintf(out, "配置目录: %s\n", c.dataDirOr())
			fmt.Fprintf(out, "凭证实现: %s\n", c.app.Secrets.Name())
			fmt.Fprintln(out, "已注册业务模块:")
			for _, k := range provider.Kinds() {
				caps, err := provider.Capabilities(k)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "  - %-10s 能力数=%d 可查询=%v\n", k, len(caps), caps.Has("sql") || caps.Has("mongo_filter"))
			}
			return nil
		},
	}
}

func (c *cli) dataDirOr() string {
	if c.dataDir != "" {
		return c.dataDir
	}
	dir, _ := config.DefaultDir()
	return dir
}
