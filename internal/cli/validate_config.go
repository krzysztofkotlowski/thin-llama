package cli

import (
	"flag"
	"fmt"
	"os"
)

func runValidateConfig(args []string) int {
	fs := flag.NewFlagSet("validate-config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultConfigPath, "path to config json")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if _, _, _, err := loadValidatedConfig(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "validate-config: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "config is valid")
	return 0
}
