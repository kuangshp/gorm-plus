package gozero

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// 为内嵌模板生成的 main 准备 go:embed 占位文件，不覆盖已经生成的文档。
// 自定义模板没有该 embed 声明时不写入额外文件。
func ensureSwaggerPlaceholder(arguments []string) error {
	if len(arguments) < 2 || arguments[0] != "api" || (arguments[1] != "go" && arguments[1] != "new") {
		return nil
	}
	for _, arg := range arguments {
		if arg == "--help" || arg == "-h" {
			return nil
		}
	}
	dir := "."
	if arguments[1] == "new" {
		// api new 的服务目录是位置参数，兼容 --style 等参数在目录前出现。
		for index := 2; index < len(arguments); index++ {
			arg := arguments[index]
			if strings.HasPrefix(arg, "-") {
				if !strings.Contains(arg, "=") {
					index++ // api new 的这些配置 flag 都带一个字符串值。
				}
				continue
			}
			dir = arg
			break
		}
	}
	for index, arg := range arguments {
		if (arg == "--dir" || arg == "-dir") && index+1 < len(arguments) {
			dir = arguments[index+1]
		}
		if strings.HasPrefix(arg, "--dir=") || strings.HasPrefix(arg, "-dir=") {
			dir = strings.SplitN(arg, "=", 2)[1]
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if !bytes.Contains(data, []byte("//go:embed swagger/swagger.json")) {
			continue
		}
		directory := filepath.Join(dir, "swagger")
		if err := os.MkdirAll(directory, 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(directory, "swagger.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		if err != nil {
			return err
		}
		// 只需同一个占位标记；详细校验和错误提示由 httpswagger 包统一处理。
		_, writeErr := file.WriteString("{\"swagger\":\"2.0\",\"info\":{\"title\":\"Swagger disabled\",\"version\":\"0\"},\"paths\":{},\"x-gormplus-placeholder\":true}\n")
		closeErr := file.Close()
		return errors.Join(writeErr, closeErr)
	}
	return nil
}
