package tunnel

import "regexp"

// Provider defines a tunnel tool preset.
type Provider struct {
	Name       string         // display name
	Binary     string         // binary to look up in PATH
	Command    string         // command template; {port} is replaced at runtime
	URLPattern *regexp.Regexp // regex with one capture group for the public URL
	URLPrefix  string         // prepended to the captured group (e.g. "http://")
}

// builtinProviders lists supported tunnel tools.
// Order matters — it's the display order in `roxy tunnel set`.
var builtinProviders = []Provider{
	{
		Name:       "ngrok",
		Binary:     "ngrok",
		Command:    "ngrok http {port} --log stdout --log-format logfmt",
		URLPattern: regexp.MustCompile(`url=(https://[^\s]+)`),
	},
	{
		Name:       "cloudflared",
		Binary:     "cloudflared",
		Command:    "cloudflared tunnel --url http://localhost:{port}",
		URLPattern: regexp.MustCompile(`\|\s+(https://[^\s]+\.trycloudflare\.com)`),
	},
	{
		Name:       "bore",
		Binary:     "bore",
		Command:    "bore local {port} --to bore.pub",
		URLPattern: regexp.MustCompile(`listening at (bore\.pub:\d+)`),
		URLPrefix:  "http://",
	},
}

// // genericURLPattern matches any http/https URL — used for custom providers.
// var genericURLPattern = regexp.MustCompile(`(https?://[^\s]+)`)

// LookupProvider returns the built-in provider with the given name, or nil.
func LookupProvider(name string) *Provider {
	for i := range builtinProviders {
		if builtinProviders[i].Name == name {
			return &builtinProviders[i]
		}
	}
	return nil
}

// BuiltinProviders returns a copy of the built-in provider list.
func BuiltinProviders() []Provider {
	out := make([]Provider, len(builtinProviders))
	copy(out, builtinProviders)
	return out
}

// // CustomProvider creates a provider from a user-supplied command template.
// func CustomProvider(command string) Provider {
// 	return Provider{
// 		Name:       "custom",
// 		Binary:     "", // no binary check for custom
// 		Command:    command,
// 		URLPattern: genericURLPattern,
// 	}
// }
