package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// urlTimeout is how long we wait for the tunnel to print a public URL.
	urlTimeout = 15 * time.Second

	// stopGracePeriod is how long we wait after SIGTERM before sending SIGKILL.
	stopGracePeriod = 5 * time.Second
)

// Tunnel manages a tunnel sidecar process.
type Tunnel struct {
	cmd       *exec.Cmd
	provider  Provider
	publicURL string
	urlCh     chan string
	done      chan struct{} // closed when process exits
	mu        sync.Mutex
}

// Start spawns the tunnel provider command and begins scanning both stdout
// and stderr for the public URL. The port placeholder {port} in the provider
// command is replaced with the given port number. All output is forwarded to
// the provided writer.
func Start(port int, provider Provider, output io.Writer) (*Tunnel, error) {
	// Check that the binary is installed (skip for custom providers)
	if provider.Binary != "" {
		if _, err := exec.LookPath(provider.Binary); err != nil {
			return nil, fmt.Errorf("%s is not installed or not in PATH\n  install it and try again", provider.Binary)
		}
	}

	cmdStr := strings.ReplaceAll(provider.Command, "{port}", fmt.Sprintf("%d", port))

	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Stdin = nil

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	t := &Tunnel{
		cmd:      cmd,
		provider: provider,
		urlCh:    make(chan string, 1),
		done:     make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start tunnel (%s): %w", provider.Name, err)
	}

	// Scan both stdout and stderr for the URL pattern.
	// Some providers (ngrok) print to stdout, others (cloudflared) to stderr.
	go t.scanOutput(stdoutPipe, output)
	go t.scanOutput(stderrPipe, output)

	// Wait for the process in the background; close(done) broadcasts to all readers.
	go func() {
		_ = cmd.Wait()
		close(t.done)
	}()

	return t, nil
}

// PublicURL blocks until the public URL is captured or the timeout expires.
// Returns the URL or an empty string if the timeout was reached.
//
// The channel urlCh is buffered(1), so only the first concurrent caller
// receives the value from it. All other concurrent callers fall through to the
// timeout/done case. To avoid them returning "" even though publicURL is
// already set, every exit path re-checks publicURL under the mutex.
func (t *Tunnel) PublicURL() string {
	t.mu.Lock()
	if t.publicURL != "" {
		t.mu.Unlock()
		return t.publicURL
	}
	t.mu.Unlock()

	select {
	case url := <-t.urlCh:
		return url
	case <-time.After(urlTimeout):
	case <-t.done:
	}

	// Re-check under the mutex: another goroutine may have received from urlCh
	// and by now scanOutput has already set publicURL.
	t.mu.Lock()
	url := t.publicURL
	t.mu.Unlock()
	return url
}

// Stop sends SIGTERM to the tunnel process and waits for it to exit.
// If it doesn't exit within the grace period, SIGKILL is sent.
func (t *Tunnel) Stop() {
	if t.cmd == nil || t.cmd.Process == nil {
		return
	}

	_ = t.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-t.done:
		return
	case <-time.After(stopGracePeriod):
		_ = t.cmd.Process.Kill()
		<-t.done
	}
}

// scanOutput reads a stream line by line, matching each against the provider's
// URL pattern. The first match (across both stdout and stderr goroutines) wins.
// All output is forwarded to the writer regardless.
func (t *Tunnel) scanOutput(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()

		// Try to capture URL (only the first match across all streams wins)
		t.mu.Lock()
		alreadyFound := t.publicURL != ""
		t.mu.Unlock()

		if !alreadyFound {
			if matches := t.provider.URLPattern.FindStringSubmatch(line); len(matches) >= 2 {
				url := t.provider.URLPrefix + matches[1]
				t.mu.Lock()
				if t.publicURL == "" {
					t.publicURL = url
					t.mu.Unlock()
					// Non-blocking send -- urlCh is buffered(1), first writer wins
					select {
					case t.urlCh <- url:
					default:
					}
				} else {
					t.mu.Unlock()
				}
			}
		}

		// Always forward output so the user can see tunnel logs
	_, _ =	fmt.Fprintln(w, line)
	}
}
