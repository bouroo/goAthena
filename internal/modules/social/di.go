// Package social is the social bounded-context module root (chat/whisper routing).
package social

import (
	"context"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/modules/social/app"
	"github.com/bouroo/goAthena/internal/modules/social/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
)

// Register provisions the ChatService into the injector. The PlayerDirectory
// port resolves from the world's live registry; the gateway's own connection
// map adapts it (social never imports gnet), so the injector-level service is
// for future cross-module chat paths — whisper routing itself runs in the
// gateway handler against its live conns.
func Register(inj do.Injector) {
	do.Provide(inj, func(inj do.Injector) (*app.ChatService, error) {
		ws, err := do.Invoke[*worldapp.WorldService](inj)
		if err != nil {
			return nil, err
		}
		return app.NewChatService(worldDirectory{ws: ws}), nil
	})
}

// worldDirectory resolves players by name through the world registry; conn
// resolution is the gateway's (it owns live connections), so it reports no
// connection and whisper senders fall back to the gateway path.
type worldDirectory struct {
	ws *worldapp.WorldService
}

// ResolveName returns the charID of the online player with that exact name.
func (d worldDirectory) ResolveName(_ context.Context, name string) (uint32, bool) {
	e, ok := d.ws.PlayerByName(name)
	if !ok {
		return 0, false
	}
	return uint32(e.ID), true //nolint:gosec // G115: charID is a uint32 value domain.
}

// ResolveConn: the world has no connection surface; see Register.
func (worldDirectory) ResolveConn(context.Context, uint32) (domain.ConnWriter, bool) {
	return nil, false
}
