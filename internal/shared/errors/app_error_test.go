//go:build unit

package errors_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharederrors "github.com/bouroo/goAthena/internal/shared/errors"
)

func TestAppError_Error(t *testing.T) {
	t.Parallel()

	t.Run("returns message when set", func(t *testing.T) {
		t.Parallel()
		e := &sharederrors.AppError{Code: "X", Message: "boom"}
		assert.Equal(t, "boom", e.Error())
	})

	t.Run("falls back to code when message is empty", func(t *testing.T) {
		t.Parallel()
		e := &sharederrors.AppError{Code: "EMPTY_MSG"}
		assert.Equal(t, "EMPTY_MSG", e.Error())
	})
}

func TestAppError_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("returns nil cause", func(t *testing.T) {
		t.Parallel()
		e := &sharederrors.AppError{Code: "X"}
		assert.Nil(t, e.Unwrap())
	})

	t.Run("returns underlying cause and supports errors.Is", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("root cause")
		e := &sharederrors.AppError{Code: "X", Message: "wrapped", Cause: sentinel}

		require.NotNil(t, e.Unwrap())
		assert.True(t, errors.Is(e, sentinel), "errors.Is must walk the Unwrap chain")
	})
}
