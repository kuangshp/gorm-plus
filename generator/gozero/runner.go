package gozero

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunOptions configures a goctl command.
type RunOptions struct {
	Executable   string
	TemplateHome string
}

// Run executes goctl with either a caller-supplied template home or the
// embedded gorm-plus templates. The returned bytes contain combined output.
func Run(ctx context.Context, options RunOptions, arguments ...string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("goctl context must not be nil")
	}
	executable := strings.TrimSpace(options.Executable)
	if executable == "" {
		var err error
		executable, err = exec.LookPath("goctl")
		if err != nil {
			return nil, fmt.Errorf("find goctl executable: %w", err)
		}
	}

	templateHome := strings.TrimSpace(options.TemplateHome)
	cleanup := func() {}
	if templateHome == "" {
		var err error
		templateHome, err = os.MkdirTemp("", "gorm-plus-goctl-templates-")
		if err != nil {
			return nil, fmt.Errorf("create temporary goctl template home: %w", err)
		}
		cleanup = func() {
			_ = os.RemoveAll(templateHome)
		}
		if err := WriteTemplateHome(templateHome); err != nil {
			cleanup()
			return nil, err
		}
	}
	defer cleanup()

	commandArguments := append([]string{}, arguments...)
	if !hasTemplateHomeArgument(commandArguments) {
		commandArguments = append(commandArguments, "--home", templateHome)
	}
	command := exec.CommandContext(ctx, executable, commandArguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("run goctl: %w", err)
	}
	return output, nil
}

func hasTemplateHomeArgument(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--home" || strings.HasPrefix(argument, "--home=") {
			return true
		}
	}
	return false
}
