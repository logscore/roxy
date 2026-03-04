package cmd

import "github.com/logscore/roxy/pkg/config"

// RunService converts a ServiceConfig into RunOptions and calls Run().
// callerOpts carries flags from the CLI (e.g. --detach, --public) that
// override or supplement the per-service config values.
func RunService(name string, svc config.ServiceConfig, callerOpts RunOptions) error {
	opts := RunOptions{
		Command:    svc.Cmd,
		Name:       svc.Name,
		StartPort:  svc.Port,
		TLS:        svc.TLS,
		Detach:     callerOpts.Detach,
		ListenPort: svc.ListenPort,
		// CLI --public flag OR per-service public flag enables tunnelling.
		Public: callerOpts.Public || svc.Public,
	}
	if opts.Name == "" {
		opts.Name = name
	}
	return Run(opts)
}
