package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"steamcli.local/steam/internal/cli"
	"steamcli.local/steam/internal/steamcmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if e := cli.New(os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx); e != nil {
		fmt.Fprintln(os.Stderr, "Error:", e)
		var x *steamcmd.ExitError
		if errors.As(e, &x) && x.Code > 0 {
			os.Exit(x.Code)
		}
		if errors.Is(e, context.Canceled) {
			os.Exit(130)
		}
		os.Exit(1)
	}
}
