//go:build unit

package infra

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
)

// writeAdapter bridges a domain.Conn (Write returns only error) to io.Writer so
// the kernel packet encoders can target a Conn directly. It mirrors
// app.writeAdapter; the transport adapters do the same at runtime.
type writeAdapter struct{ c domain.Conn }

func (w writeAdapter) Write(p []byte) (int, error) {
	if err := w.c.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// freePort returns an OS-assigned free TCP port. It opens then immediately
// closes a listener to discover one; the brief close-before-bind window is an
// acceptable TOCTOU for self-contained loopback tests.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitForListen polls the address until a TCP dial succeeds or the timeout
// elapses. It bridges the gap between starting a server in a goroutine and the
// listener actually accepting.
func waitForListen(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("listener %s never accepted within %s", addr, timeout)
}
