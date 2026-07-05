//go:build unit

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantCmd      string
		wantCount    int
		wantForceVer int
		wantErr      bool
		errContains  string
	}{
		{
			name:      "no args defaults to up",
			args:      nil,
			wantCmd:   "up",
			wantCount: 1,
		},
		{
			name:      "empty slice defaults to up",
			args:      []string{},
			wantCmd:   "up",
			wantCount: 1,
		},
		{
			name:      "explicit up",
			args:      []string{"up"},
			wantCmd:   "up",
			wantCount: 1,
		},
		{
			name:      "down defaults count to 1",
			args:      []string{"down"},
			wantCmd:   "down",
			wantCount: 1,
		},
		{
			name:      "down with positive count",
			args:      []string{"down", "3"},
			wantCmd:   "down",
			wantCount: 3,
		},
		{
			name:        "down with zero count is rejected",
			args:        []string{"down", "0"},
			wantCmd:     "down",
			wantErr:     true,
			errContains: "invalid down count",
		},
		{
			name:        "down with negative count is rejected",
			args:        []string{"down", "-1"},
			wantCmd:     "down",
			wantErr:     true,
			errContains: "invalid down count",
		},
		{
			name:        "down with non-integer count is rejected",
			args:        []string{"down", "abc"},
			wantCmd:     "down",
			wantErr:     true,
			errContains: "invalid down count",
		},
		{
			name:         "force with version",
			args:         []string{"force", "5"},
			wantCmd:      "force",
			wantCount:    1,
			wantForceVer: 5,
		},
		{
			name:        "force without version is rejected",
			args:        []string{"force"},
			wantCmd:     "force",
			wantErr:     true,
			errContains: "force requires a version",
		},
		{
			name:        "force with non-integer version is rejected",
			args:        []string{"force", "abc"},
			wantCmd:     "force",
			wantErr:     true,
			errContains: "invalid force version",
		},
		{
			name:      "version command",
			args:      []string{"version"},
			wantCmd:   "version",
			wantCount: 1,
		},
		{
			name:      "unknown command is returned without error",
			args:      []string{"unknown"},
			wantCmd:   "unknown",
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd, count, forceVersion, err := parseArgs(tt.args)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCmd, cmd)
			assert.Equal(t, tt.wantCount, count)
			assert.Equal(t, tt.wantForceVer, forceVersion)
		})
	}
}

func TestRun_ParseArgsErrorReturnsExitOne(t *testing.T) {
	t.Parallel()

	exitCode := run([]string{"force"})
	assert.Equal(t, 1, exitCode)
}
