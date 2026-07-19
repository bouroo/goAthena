//go:build unit || integration

package athenaconf

import "github.com/bouroo/goAthena/internal/config"

// newTestConfig returns a zero-value config.Config with required
// sub-structs initialised so mapstructure tags work as expected. Tests
// that exercise the translator's mapping table use this instead of
// constructing Config literals so future Config additions don't silently
// break the unit tests.
func newTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Identity.UseMD5Passwords = false
	cfg.Identity.MaxChars = 0
	return cfg
}
