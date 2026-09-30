package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/postscale/postscale-cli/internal/cli"
)

var version = "1.0.0"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version)
	stop()
	os.Exit(code)
}
