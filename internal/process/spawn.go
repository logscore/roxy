package process

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/logscore/roxy/internal/tunnel"
	"github.com/logscore/roxy/pkg/config"
)

// Run spawns the command with PORT set, tracks the route, optionally
// starts a tunnel sidecar, and handles cleanup on exit or signal.
// localURL is the formatted local URL (e.g. "https://main.my-app.test") used
// for display when --public is set (both URLs printed together).
func Run(id string, cmdStr string, port int, domain string, tlsEnabled bool, listenPort int, tunnelProvider *tunnel.Provider, localURL string, store *config.Store, configDir string, logFile string) error {
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	routeType := "http"
	if listenPort > 0 {
		routeType = "tcp"
	}

	// Track route (the proxy watches routes.json for changes)
	if err := store.AddRoute(config.Route{
		ID:         id,
		Domain:     domain,
		Port:       port,
		ListenPort: listenPort,
		Type:       routeType,
		TLS:        tlsEnabled,
		Command:    cmdStr,
		LogFile:    logFile,
		Public:     tunnelProvider != nil,
		Created:    time.Now(),
	}); err != nil {
		return fmt.Errorf("failed to register route: %w", err)
	}

	// Track the tunnel so cleanup can stop it
	var tun *tunnel.Tunnel

	// Ensure cleanup on any exit path
	cleanup := func() {
		// Stop tunnel first (if running)
		if tun != nil {
			tun.Stop()
		}

		if err := store.RemoveRoute(domain); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove route: %v\n", err)
		}
	}
	defer cleanup()

	// Spawn child process
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		"HOST=127.0.0.1",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Update route with PID (atomic — no gap where proxy sees no route)
	_ = store.UpdateRoute(domain, func(r *config.Route) {
		r.PID = cmd.Process.Pid
	})

	// Start tunnel sidecar if a provider was given
	if tunnelProvider != nil {
		var err error
		tun, err = tunnel.Start(port, *tunnelProvider, io.Discard)
		if err != nil {
			// Tunnel failed — still print the local URL and continue
			fmt.Println()
			fmt.Printf("  \x1b[90mlocal\x1b[0m   %s\n", localURL)
			fmt.Println()
			fmt.Fprintf(os.Stderr, "warning: failed to start tunnel: %v\n", err)
			fmt.Fprintf(os.Stderr, "  the dev server is running, but no public URL is available\n\n")
		} else {
			// Wait for the public URL (blocks up to ~15s)
			publicURL := tun.PublicURL()
			fmt.Println()
			fmt.Printf("  \x1b[90mlocal:\x1b[0m   %s\n", localURL)
			if publicURL != "" {
				fmt.Printf("  \x1b[90mremote:\x1b[0m  %s\n", publicURL)
				_ = store.UpdateRoute(domain, func(r *config.Route) {
					r.PublicURL = publicURL
				})
			} else {
				fmt.Fprintf(os.Stderr, "  \x1b[33mtunnel\x1b[0m  could not detect public URL\n")
			}
			fmt.Println()
		}
	}

	// Wait for either signal or process exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case sig := <-sigChan:
		_ = cmd.Process.Signal(sig)

		// If a second signal arrives during cleanup, force-kill the process
		select {
		case <-sigChan:
			fmt.Println("\nForce killing process...")
			_ = cmd.Process.Kill()
			<-done
		case <-done:
		}
		return nil

	case err := <-done:
		if err != nil {
			return fmt.Errorf("command exited with error: %w", err)
		}
		return nil
	}
}
