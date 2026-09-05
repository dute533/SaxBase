package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"saxbase/internal/cli"
	"saxbase/internal/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Run(ctx, os.Args[1:], os.Getenv, os.Stdout, migrations.Open); err != nil {
		fmt.Fprintln(os.Stderr, "saxbase:", err)
		os.Exit(1)
	}
}
