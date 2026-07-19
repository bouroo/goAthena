package athenaconf

import (
	"os"
	"path/filepath"
)

// DefaultRootDir returns the rAthena checkout root inferred from a
// source directory of conf files. If srcDir is third_party/rathena/conf,
// the root is third_party/rathena; otherwise srcDir itself is returned
// as the root.
//
// This helper backs the --root default in cmd/import-conf; callers can
// always override it explicitly.
func DefaultRootDir(srcDir string) string {
	candidate := filepath.Join(srcDir, "..")
	if _, err := os.Stat(filepath.Join(candidate, "conf")); err == nil {
		return candidate
	}
	return srcDir
}
