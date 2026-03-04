package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/logscore/roxy/internal/platform"
	"github.com/logscore/roxy/internal/tunnel"
	"github.com/logscore/roxy/pkg/config"
)

// TunnelSet runs an interactive provider selection and saves it to global config.
func TunnelSet() error {
	p := platform.Detect()
	paths := platform.GetPaths(p)

	providers := tunnel.BuiltinProviders()

	fmt.Println()
	fmt.Println("  Select a tunnel provider:")
	fmt.Println()
	for i, prov := range providers {
		fmt.Printf("    %d) %s\n", i+1, prov.Name)
	}
	// fmt.Printf("    %d) custom\n", len(providers)+1)
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("  Choice: ")
	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}
	input = strings.TrimSpace(input)

	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(providers) {
		return fmt.Errorf("invalid choice: %s", input)
	}

	cfg, err := config.LoadGlobalConfig(paths.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// if choice == len(providers)+1 {
	// 	// Custom provider
	// 	fmt.Println()
	// 	fmt.Println("  Enter the tunnel command (use {port} as placeholder):")
	// 	fmt.Print("  > ")
	// 	cmdStr, err := reader.ReadString('\n')
	// 	if err != nil {
	// 		return fmt.Errorf("failed to read input: %w", err)
	// 	}
	// 	cmdStr = strings.TrimSpace(cmdStr)
	// 	if cmdStr == "" {
	// 		return fmt.Errorf("command cannot be empty")
	// 	}
	// 	if !strings.Contains(cmdStr, "{port}") {
	// 		return fmt.Errorf("command must contain {port} placeholder")
	// 	}
	// 	cfg.Tunnel = config.TunnelConfig{
	// 		Provider: "custom",
	// 		Command:  cmdStr,
	// 	}
	// } else {
	selected := providers[choice-1]
	cfg.Tunnel = config.TunnelConfig{
		Provider: selected.Name,
	}
	// }

	if err := config.SaveGlobalConfig(paths.ConfigDir, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("  Tunnel provider set to: %s\n", cfg.Tunnel.Provider)
	fmt.Println()
	return nil
}

// TunnelStatus shows the current tunnel provider configuration.
func TunnelStatus() error {
	p := platform.Detect()
	paths := platform.GetPaths(p)

	cfg, err := config.LoadGlobalConfig(paths.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Tunnel.Provider == "" {
		fmt.Println()
		fmt.Println("  No tunnel provider configured.")
		fmt.Println("  Run 'roxy tunnel set' to choose one.")
		fmt.Println()
		return nil
	}

	fmt.Println()
	fmt.Printf("  provider:  %s\n", cfg.Tunnel.Provider)

	// if cfg.Tunnel.Provider == "custom" {
	// 	fmt.Printf("  command:   %s\n", cfg.Tunnel.Command)
	// } else {
	prov := tunnel.LookupProvider(cfg.Tunnel.Provider)
	if prov != nil {
		// Check if binary is installed
		binaryPath, err := exec.LookPath(prov.Binary)
		if err != nil {
			fmt.Printf("  binary:    %s (not found in PATH)\n", prov.Binary)
		} else {
			fmt.Printf("  binary:    %s\n", binaryPath)
		}
	} else {
		fmt.Printf("  warning:   %q is not a recognized provider\n", cfg.Tunnel.Provider)
		fmt.Println("  Run 'roxy tunnel set' to choose a supported provider.")
	}
	// }

	fmt.Println()
	return nil
}
