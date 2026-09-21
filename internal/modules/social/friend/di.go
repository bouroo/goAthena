// Package friend is the friend bounded-context module root.
//
// Friendships are (char_id, friend_id) pairs in the shared `friends` table, so
// this module reads and writes the char table through its own repository
// rather than through the character module.
package friend

import (
	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/social/friend/app"
	"github.com/bouroo/goAthena/internal/modules/social/friend/domain"
	"github.com/bouroo/goAthena/internal/modules/social/friend/infra"
)

// Register provisions the GORM friend repo and the FriendService into the
// injector. The GORM handle is resolved lazily so a down database surfaces as
// a resolution error at use time rather than at boot.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMFriendRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMFriendRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.FriendRepository, error) {
		return do.MustInvoke[*infra.GORMFriendRepository](i), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.FriendService, error) {
		repo := do.MustInvoke[*infra.GORMFriendRepository](i)
		return app.NewFriendService(repo), nil
	})
}
