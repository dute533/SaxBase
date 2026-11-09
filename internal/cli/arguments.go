package cli

import (
	"fmt"
	"strings"
)

var booleanOptions = map[string]bool{
	"f": true, "force": true, "yes": true, "version": true,
}

var valueOptions = map[string]bool{
	"config": true, "dir": true, "manifest": true, "objects-dir": true, "target": true,
}

// reorderOptions lets global options appear before or after a command while
// preserving positional command arguments for the existing command parser.
func reorderOptions(args []string) ([]string, error) {
	options := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		nameValue := strings.TrimLeft(arg, "-")
		name, _, assigned := strings.Cut(nameValue, "=")
		if booleanOptions[name] {
			options = append(options, arg)
			continue
		}
		if !valueOptions[name] {
			positional = append(positional, arg)
			continue
		}
		options = append(options, arg)
		if assigned {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("option %s requires a value", arg)
		}
		i++
		options = append(options, args[i])
	}
	return append(options, positional...), nil
}
