//go:build unit

package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/bouroo/goAthena/internal/config"
)

func TestReportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "fatal config error gets distinct label",
			err:  fmt.Errorf("load config: %w", config.ErrConfigFatal),
			want: "goathena: fatal configuration error: load config: config fatal\n",
		},
		{
			name: "non-fatal error keeps generic prefix",
			err:  errors.New("load config: transient i/o"),
			want: "goathena: load config: transient i/o\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			reportError(&buf, tt.err)
			if got := buf.String(); got != tt.want {
				t.Errorf("reportError() = %q, want %q", got, tt.want)
			}
		})
	}
}
