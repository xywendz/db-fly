//go:build windows && cgo

package main

import (
	"fmt"
	"os"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/gui"
)

func main() {
	if err := gui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", domain.UserMessage(err))
		os.Exit(1)
	}
}
