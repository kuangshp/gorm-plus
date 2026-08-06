package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/kuangshp/gorm-plus/generator/gozero"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return usageError()
	}

	switch arguments[0] {
	case "goctl":
		if len(arguments) == 1 {
			return errors.New("goctl arguments must not be empty")
		}
		output, err := gozero.Run(context.Background(), gozero.RunOptions{}, arguments[1:]...)
		if len(output) > 0 {
			_, _ = os.Stdout.Write(output)
		}
		return err
	case "export-goctl-templates":
		flags := flag.NewFlagSet("export-goctl-templates", flag.ContinueOnError)
		output := flags.String("output", "", "goctl template destination")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		return gozero.WriteTemplateHome(*output)
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New("usage: gormplus goctl <goctl arguments...> | gormplus export-goctl-templates --output <directory>")
}
