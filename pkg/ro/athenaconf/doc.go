// Package athenaconf parses rAthena .conf files into a typed key/value
// representation and overlays the parsed values onto a config.Config.
//
// rAthena's libconfig syntax is a tiny subset of YAML: line-per-key,
// `key: value`, `//` line and inline comments, `import: "path"` directives,
// and boolean values written as yes/no/on/off/true/false. This package
// implements that subset plus a YAML overlay writer used by the
// cmd/import-conf subcommand.
//
// The package is intentionally import-time only (decision D-007): runtime
// configuration is always YAML loaded by internal/config. The translator
// never appears in the hot path of any service.
package athenaconf
