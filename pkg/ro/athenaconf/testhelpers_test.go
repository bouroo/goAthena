//go:build unit || integration

package athenaconf

// newTestConfig returns a zero-value athenaconf.Config ready to receive
// mapped values. Tests that exercise the mapping table use this instead of
// constructing Config literals so future Config additions don't silently
// break the unit tests.
func newTestConfig() *Config {
	return &Config{}
}
