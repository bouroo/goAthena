// Command loadgen is a minimal TCP load harness for the goathena login
// endpoint. It opens N concurrent connections to LOGIN_HOST:LOGIN_PORT and
// fires CA_LOGIN frames at a configurable rate, then reports throughput and
// outcome counters at SIGINT. It is intentionally tiny — no third-party
// dependencies, no parallel test scaffolding — so an operator can use it
// from any workstation to verify the login listener's stated throughput
// under load.
//
// Usage:
//
//	loadgen -host 127.0.0.1 -port 6900 -conns 50 -duration 30s -rate 5
//
// The default -user/-pass values exercise the success path when the server
// has been seeded with that account (e.g. via the migrations + a sql
// INSERT). A wrong password exercises the failure path and the rate
// limiter (the limiter defaults to 5 attempts per IP per burst).
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

func main() {
	if err := run(); err != nil {
		log.SetOutput(os.Stderr)
		log.Println("loadgen:", err)
		os.Exit(1)
	}
}

type config struct {
	host     string
	port     int
	conns    int
	duration time.Duration
	rate     float64
	user     string
	pass     string
	verbose  bool
}

func run() error {
	cfg := parseFlags()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if cfg.verbose {
		log.Printf("loadgen: %d conns × %.1f login/s for %s → %s:%d (user=%q)",
			cfg.conns, cfg.rate, cfg.duration, cfg.host, cfg.port, cfg.user)
	}

	var (
		success  atomic.Uint64
		refused  atomic.Uint64
		errors   atomic.Uint64
		throttle atomic.Uint64
	)
	deadline := time.Now().Add(cfg.duration)

	var wg sync.WaitGroup
	for i := 0; i < cfg.conns; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			runClient(ctx, id, cfg, deadline, &success, &refused, &errors, &throttle)
		}(i)
	}
	wg.Wait()

	fmt.Printf("\nloadgen summary:\n")
	fmt.Printf("  duration : %s\n", cfg.duration)
	fmt.Printf("  conns    : %d\n", cfg.conns)
	fmt.Printf("  rate     : %.1f login/s\n", cfg.rate)
	fmt.Printf("  success  : %d\n", success.Load())
	fmt.Printf("  refused  : %d\n", refused.Load())
	fmt.Printf("  throttle : %d\n", throttle.Load())
	fmt.Printf("  errors   : %d\n", errors.Load())
	return nil
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.host, "host", envOr("LOADGEN_HOST", "127.0.0.1"), "login host")
	flag.IntVar(&cfg.port, "port", envInt("LOADGEN_PORT", 6900), "login port")
	flag.IntVar(&cfg.conns, "conns", envInt("LOADGEN_CONNS", 10), "concurrent connections")
	flag.DurationVar(&cfg.duration, "duration", envDuration("LOADGEN_DURATION", 10*time.Second), "test duration")
	flag.Float64Var(&cfg.rate, "rate", envFloat("LOADGEN_RATE", 2.0), "login attempts/sec per connection")
	flag.StringVar(&cfg.user, "user", envOr("LOADGEN_USER", "loadgen"), "username to send")
	flag.StringVar(&cfg.pass, "pass", envOr("LOADGEN_PASS", "loadgen"), "password to send")
	flag.BoolVar(&cfg.verbose, "v", false, "verbose logging")
	flag.Parse()
	return cfg
}

func envOr(k, fb string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fb
}

func envInt(k string, fb int) int {
	if v := os.Getenv(k); v != "" {
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err == nil {
			return n
		}
	}
	return fb
}

func envFloat(k string, fb float64) float64 {
	if v := os.Getenv(k); v != "" {
		var f float64
		_, err := fmt.Sscanf(v, "%f", &f)
		if err == nil {
			return f
		}
	}
	return fb
}

func envDuration(k string, fb time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fb
}

// runClient opens one TCP connection, paces login attempts at cfg.rate, and
// counts outcomes until the deadline or ctx cancellation. The frame is the
// real CA_LOGIN wire shape so the load exercises the packet DB and codec
// paths — no shortcuts.
func runClient(ctx context.Context, id int, cfg config, deadline time.Time, success, refused, errors, throttle *atomic.Uint64) {
	addr := net.JoinHostPort(cfg.host, fmt.Sprintf("%d", cfg.port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		if cfg.verbose {
			log.Printf("[%d] dial: %v", id, err)
		}
		errors.Add(1)
		return
	}
	defer func() { _ = conn.Close() }()

	// Pace login attempts: interval = 1/rate.
	interval := time.Duration(float64(time.Second) / cfg.rate)
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	frame := encodeCALoginFrame(cfg.user, cfg.pass)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().After(deadline) {
				return
			}
			outcome := sendAndClassify(ctx, conn, frame)
			switch outcome {
			case outcomeAccept:
				success.Add(1)
			case outcomeRefuse:
				refused.Add(1)
			case outcomeThrottle:
				throttle.Add(1)
			default:
				errors.Add(1)
			}
			if cfg.verbose {
				log.Printf("[%d] outcome=%s", id, outcome)
			}
		}
	}
}

type outcome string

const (
	outcomeAccept   outcome = "accept"
	outcomeRefuse   outcome = "refuse"
	outcomeThrottle outcome = "throttle"
	outcomeError    outcome = "error"
)

// sendAndClassify writes one CA_LOGIN frame and reads the first 27-byte
// reply. AC_ACCEPT_LOGIN = 0x0ac4 (header is "ACCEPT LOGIN"); AC_REFUSE_LOGIN
// = 0x006a. Anything else is "error" (likely throttle — server closed the
// conn without writing).
func sendAndClassify(ctx context.Context, conn net.Conn, frame []byte) outcome {
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return outcomeError
	}
	if _, err := conn.Write(frame); err != nil {
		return outcomeError
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return outcomeError
	}
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(ctxReader(ctx, conn), hdr); err != nil {
		// A limiter denial is SILENT by design (no reply, so an attacker
		// cannot probe the limiter), which the client observes as a read
		// timeout. Classify that as throttle; any other read failure is an
		// error.
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return outcomeThrottle
		}
		return outcomeError
	}
	cmd := binary.LittleEndian.Uint16(hdr)
	// Drain the remainder of the reply frame: the server keeps the connection
	// open, so leftover bytes from reply N would misalign the read of reply
	// N+1. The accept (0x0ac4) carries its total wire length at offset 2; the
	// refuse (0x083e) is fixed-size.
	drain := 0
	switch cmd {
	case ropacket.HeaderACACCEPTLOGIN: // 0x0ac4 (>=20170315)
		lenBuf := make([]byte, 2)
		if _, err := io.ReadFull(ctxReader(ctx, conn), lenBuf); err != nil {
			return outcomeError
		}
		total := int(binary.LittleEndian.Uint16(lenBuf))
		if total < 4 {
			return outcomeError
		}
		drain = total - 4
	case ropacket.HeaderACREFUSELOGIN: // 0x083e (>=20170315; legacy 0x006a)
		drain = ropacket.RefuseLoginResponse{}.Size() - 4
	default:
		// The rate limiter drops silently (no reply at all), so an unknown
		// opcode means the protocol answered off-script — classify as error
		// rather than guessing (the stream is unaligned; the caller abandons
		// the connection on the next failed exchange).
		return outcomeError
	}
	if drain > 0 {
		if _, err := io.CopyN(io.Discard, ctxReader(ctx, conn), int64(drain)); err != nil {
			return outcomeError
		}
	}
	switch cmd {
	case ropacket.HeaderACACCEPTLOGIN:
		return outcomeAccept
	case ropacket.HeaderACREFUSELOGIN:
		return outcomeRefuse
	default:
		return outcomeError
	}
}

// ctxReader returns an io.Reader that respects ctx cancellation by closing
// the conn early. We can't plumb ctx into ReadFull directly, so this is the
// cheapest cancellation hook.
type ctxConn struct {
	ctx context.Context
	c   net.Conn
}

func (cc ctxConn) Read(p []byte) (int, error) {
	if err := cc.ctx.Err(); err != nil {
		return 0, fmt.Errorf("ctxConn: cancelled: %w", err)
	}
	n, err := cc.c.Read(p)
	if err != nil {
		return n, fmt.Errorf("ctxConn: read: %w", err)
	}
	return n, nil
}

func ctxReader(ctx context.Context, c net.Conn) io.Reader { return ctxConn{ctx: ctx, c: c} }

// encodeCALoginFrame returns the wire bytes for CA_LOGIN. The kernel's
// CALoginRequest.Encode writes the complete frame including the 0x0064 cmd
// header (55 bytes) — prepending another header here produced a 57-byte
// frame the server's fixed-size framing misread (empty username, then
// 0x0000 garbage frames), which is exactly the class of bug this tool
// exists to catch.
func encodeCALoginFrame(user, pass string) []byte {
	req := ropacket.CALoginRequest{
		Version:    55,
		Username:   padOrTrunc(user, 24),
		Password:   padOrTrunc(pass, 24),
		ClientType: 0,
	}
	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		// Encoding is a memory copy; it cannot fail in practice for sane inputs.
		return nil
	}
	return buf.Bytes()
}

// padOrTrunc right-pads user/pass to n bytes with NULs (the legacy wire
// format). Shorter inputs are padded; longer ones are truncated — both
// match rAthena's char_array storage.
func padOrTrunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	if len(s) == n {
		return s
	}
	out := make([]byte, n)
	copy(out, s)
	return string(out)
}
