package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/logscore/roxy/internal/domain"
	"github.com/logscore/roxy/internal/platform"
	"github.com/logscore/roxy/internal/port"
	"github.com/logscore/roxy/internal/process"
	"github.com/logscore/roxy/internal/proxy"
	"github.com/logscore/roxy/internal/tunnel"
	"github.com/logscore/roxy/pkg/config"
)

type RunOptions struct {
	Command    string
	StartPort  int
	Name       string
	TLS        bool
	Detach     bool
	LogFile    string
	ID         string // internal: passed from parent when re-execing in detach mode
	ListenPort int    // TCP mode: proxy listens on this port and forwards to the service
	Public     bool   // expose via tunnel (requires configured provider)
}

// LogsDir returns the path to the logs directory.
func LogsDir(configDir string) string {
	return filepath.Join(configDir, "logs")
}

func Run(opts RunOptions) error {
	p := platform.Detect()
	paths := platform.GetPaths(p)

	// Ensure config directory exists
	if err := os.MkdirAll(paths.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	// Auto-configure DNS resolver on first run
	if !platform.ResolverConfigured(p, paths, 1299) {
		if err := platform.ConfigureResolver(p, paths, 1299); err != nil {
			return fmt.Errorf("failed to configure DNS resolver: %w", err)
		}
		fmt.Println("done - DNS configured")
		fmt.Println()
	}

	// Auto-start proxy if not running
	if !proxy.IsRunning(paths.ConfigDir) {
		if err := ProxyStart(ProxyOptions{HTTPPort: 80, TLS: true, HTTPSPort: 443, DNSPort: 1299}); err != nil {
			return fmt.Errorf("failed to start proxy: %w", err)
		}
		for range proxy.ProxyStartRetries {
			time.Sleep(proxy.ProxyStartRetryInterval)
			if proxy.IsRunning(paths.ConfigDir) {
				break
			}
		}
		if !proxy.IsRunning(paths.ConfigDir) {
			return fmt.Errorf("proxy failed to start -- check if port 80 is in use")
		}
	}

	// Auto-trust CA cert on first --tls use
	if opts.TLS {
		caCertPath := paths.CertsDir + "/ca-cert.pem"
		if !platform.CATrusted(p, caCertPath) {
			// The proxy generates certs on startup, wait for the CA cert to appear
			for range 30 {
				if _, err := os.Stat(caCertPath); err == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if _, err := os.Stat(caCertPath); err == nil {
				if err := platform.TrustCA(p, caCertPath); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to trust CA cert: %v\n", err)
					fmt.Fprintf(os.Stderr, "HTTPS may show certificate warnings in browsers.\n\n")
				} else {
					fmt.Println("done - CA certificate trusted")
					fmt.Println()
				}
			}
		}
	}

	store := config.NewStore(paths.RoutesFile)

	// Prune routes for processes that are no longer alive
	if pruned, err := store.PruneStaleRoutes(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to prune stale routes: %v\n", err)
	} else if pruned > 0 {
		fmt.Printf("cleaned up %d stale route(s)\n", pruned)
	}

	// Find available port (checks both OS and routes.json)
	assignedPort, err := port.Find(opts.StartPort, paths.RoutesFile)
	if err != nil {
		return fmt.Errorf("failed to find available port: %w", err)
	}

	// Generate domain
	dom, err := domain.Generate(opts.Name)
	if err != nil {
		return fmt.Errorf("failed to generate domain: %w", err)
	}

	// Check for domain conflict with an already-running process
	if existing := store.FindRoute(dom); existing != nil {
		return fmt.Errorf(
			"domain %s is already in use (pid %d, port %d); to run another service on this project, use --name: roxy run %q --name <service-name>",
			dom, existing.PID, existing.Port, opts.Command,
		)
	}

	scheme := "http"
	if opts.TLS {
		scheme = "https"
	}

	id := opts.ID
	if id == "" {
		id = config.GenerateID(dom)
	}

	// Resolve tunnel provider if --public is set
	var tunnelProvider *tunnel.Provider
	if opts.Public {
		globalCfg, err := config.LoadGlobalConfig(paths.ConfigDir)
		if err != nil {
			return fmt.Errorf("failed to load global config: %w", err)
		}
		if globalCfg.Tunnel.Provider == "" {
			return fmt.Errorf("no tunnel provider configured\n  run 'roxy tunnel set' to choose one")
		}

		// if globalCfg.Tunnel.Provider == "custom" {
		// 	if globalCfg.Tunnel.Command == "" {
		// 		return fmt.Errorf("custom tunnel provider has no command configured\n  run 'roxy tunnel set' to fix")
		// 	}
		// 	prov := tunnel.CustomProvider(globalCfg.Tunnel.Command)
		// 	tunnelProvider = &prov
		// } else {
		prov := tunnel.LookupProvider(globalCfg.Tunnel.Provider)
		if prov == nil {
			return fmt.Errorf("unknown tunnel provider: %s\n  run 'roxy tunnel set' to reconfigure", globalCfg.Tunnel.Provider)
		}
		tunnelProvider = prov
		// }
	}

	// Detached mode: re-exec ourselves without -d, in a new session with log output
	if opts.Detach {
		return runDetached(opts, paths, dom, id, assignedPort, scheme)
	}

	localURL := fmt.Sprintf("%s://%s", scheme, dom)
	if opts.ListenPort > 0 {
		localURL = fmt.Sprintf("%s (tcp :%d → :%d)", dom, opts.ListenPort, assignedPort)
	}

	// Print URL up front unless --public (spawn.go prints local + tunnel together after tunnel connects)
	if !opts.Public {
		fmt.Println()
		fmt.Printf("  %s\n", localURL)
		fmt.Println()
	}

	return process.Run(id, opts.Command, assignedPort, dom, opts.TLS, opts.ListenPort, tunnelProvider, localURL, store, paths.ConfigDir, opts.LogFile)
}

func runDetached(opts RunOptions, paths platform.Paths, dom string, id string, assignedPort int, scheme string) error {
	logsDir := LogsDir(paths.ConfigDir)
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs dir: %w", err)
	}

	logPath := filepath.Join(logsDir, dom+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	// Re-exec: roxy run "<command>" [flags] (without --detach/-d)
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}

	args := []string{"run", opts.Command}
	args = append(args, "--port", fmt.Sprintf("%d", assignedPort))
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	if opts.TLS {
		args = append(args, "--tls")
	}
	if opts.ListenPort > 0 {
		args = append(args, "--listen-port", fmt.Sprintf("%d", opts.ListenPort))
	}
	if opts.Public {
		args = append(args, "--public")
	}
	// Pass internal flags so the child records them in the route
	args = append(args, "--log-file", logPath)
	args = append(args, "--id", id)

	cmd := exec.Command(exePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start detached process: %w", err)
	}

	localURL := fmt.Sprintf("%s://%s", scheme, dom)
	if opts.ListenPort > 0 {
		localURL = fmt.Sprintf("%s (tcp :%d → :%d)", dom, opts.ListenPort, assignedPort)
	}

	// If --public, wait for the child to write the tunnel URL to routes.json
	publicURL := ""
	if opts.Public {
		store := config.NewStore(paths.RoutesFile)
		for range 30 { // poll for up to ~15s (30 * 500ms)
			time.Sleep(500 * time.Millisecond)
			if r := store.FindRoute(dom); r != nil && r.PublicURL != "" {
				publicURL = r.PublicURL
				break
			}
		}
	}

	fmt.Println()
	if opts.Public {
		fmt.Printf("  \x1b[90mlocal:\x1b[0m   %s\n", localURL)
		if publicURL != "" {
			fmt.Printf("  \x1b[90mremote:\x1b[0m  %s\n", publicURL)
		} else {
			fmt.Printf("  \x1b[33mremote:\x1b[0m  waiting... (check logs)\n")
		}
	} else {
		fmt.Printf("  %s\n", localURL)
	}
	fmt.Println()
	fmt.Printf("  \x1b[90mlogs\x1b[0m    %s\n", logPath)
	fmt.Println()

	return nil
}
