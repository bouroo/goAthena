//go:build unit

package di_test

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/gateway/di"
)

func TestRegister_ReturnsNil(t *testing.T) {
	t.Parallel()
	require.NoError(t, di.Register(do.New()))
}
