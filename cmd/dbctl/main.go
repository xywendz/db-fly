// dbctl 是纯 Go 命令行工具，可在 Linux 下 CGO_ENABLED=0 静态编译。
package main

import (
	"fmt"
	"os"

	"github.com/dbclient/dbclient/internal/cli"
	"github.com/dbclient/dbclient/internal/domain"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", domain.UserMessage(err))
		os.Exit(1)
	}
}
