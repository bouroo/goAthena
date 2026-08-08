// Package social is the social bounded-context module root (chat/whisper routing).
package social

import (
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/modules/social/app"
)

// Register provisions the ChatService into the injector. The PlayerDirectory
// port resolves from the world's connection registry when it's built.
func Register(inj do.Injector) {
	do.Provide(inj, func(_ do.Injector) (*app.ChatService, error) {
		// PlayerDirectory wiring lands when the world connection registry is built.
		// For now ChatService is architecturally in place; the dispatch handler
		// can build it ad-hoc with a concrete directory once conns are tracked.
		return app.NewChatService(nil), nil
	})
}
