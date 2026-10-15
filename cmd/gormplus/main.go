package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kuangshp/gorm-plus/generator/gozero"
	swaggergen "github.com/kuangshp/gorm-plus/generator/swagger"
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
	case "swagger-open":
		flags := flag.NewFlagSet("swagger-open", flag.ContinueOnError)
		configFile := flags.String("config", "", "服务 YAML 配置路径，相对于 --dir")
		dir := flags.String("dir", ".", "服务目录")
		timeout := flags.Duration("timeout", 30*time.Second, "等待文档就绪的最长时间")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *timeout <= 0 {
			return errors.New("swagger-open 不接受位置参数，--timeout 必须大于 0")
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		output, err := swaggergen.Open(ctx, swaggergen.Options{ConfigFile: *configFile, Dir: *dir})
		if len(output) > 0 {
			_, _ = os.Stdout.Write(output)
		}
		return err
	case "swagger":
		flags := flag.NewFlagSet("swagger", flag.ContinueOnError)
		configFile := flags.String("config", "", "服务 YAML 配置路径，相对于 --dir")
		dir := flags.String("dir", ".", "服务目录，也是 ApiFile 相对路径的基准")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("swagger 命令不接受位置参数")
		}
		output, err := swaggergen.Generate(context.Background(), swaggergen.Options{ConfigFile: *configFile, Dir: *dir})
		if len(output) > 0 {
			_, _ = os.Stdout.Write(output)
		}
		return err
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
	return errors.New("usage: gormplus goctl <goctl arguments...> | gormplus swagger --config <service.yaml> [--dir <service directory>] | gormplus swagger-open --config <service.yaml> [--timeout 30s] | gormplus export-goctl-templates --output <directory>")
}
