// Package quest is the quest (persistent NPC variables) bounded-context
// module root. QuestVars are the storage behind rAthena's `set` and
// `getvariableofnpc` script builtins.
package quest

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	contentdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/quest/app"
	"github.com/bouroo/goAthena/internal/modules/content/quest/domain"
	"github.com/bouroo/goAthena/internal/modules/content/quest/infra"
)

// Register provisions the GORM quest repo and the QuestService into the
// injector. Composition root (composition.go) calls this once at boot.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMQuestRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMQuestRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.QuestRepository, error) {
		return do.MustInvoke[*infra.GORMQuestRepository](i), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.QuestService, error) {
		repo := do.MustInvoke[*infra.GORMQuestRepository](i)
		return app.NewQuestService(repo), nil
	})
	// Expose the QuestService as a content-domain ScriptQuest port so the
	// content Engine (and any future script-VM consumer) can resolve it
	// without depending on the quest/app package directly. The adapter is a
	// thin wrapper: the ScriptQuest port's signatures (no error on GetVar)
	// are the right shape for a builtin return value, while the QuestService
	// returns errors so callers can choose to log them.
	do.Provide(inj, func(i do.Injector) (contentdomain.ScriptQuest, error) {
		svc := do.MustInvoke[*app.QuestService](i)
		return scriptQuestAdapter{svc: svc}, nil
	})
}

// scriptQuestAdapter adapts *app.QuestService (which returns errors) to the
// content/domain.ScriptQuest port (which returns int64 directly). The port
// shape is the right one for script-VM builtins — a script author doesn't
// branch on a write error inside a `setquestvar` call.
type scriptQuestAdapter struct {
	svc *app.QuestService
}

func (a scriptQuestAdapter) GetVar(charID uint32, npcName, varName string) int64 {
	v, err := a.svc.GetVar(context.TODO(), charID, npcName, varName)
	if err != nil {
		return 0
	}
	return v
}

func (a scriptQuestAdapter) SetVar(charID uint32, npcName, varName string, value int64) error {
	if err := a.svc.SetVar(context.TODO(), charID, npcName, varName, value); err != nil {
		return fmt.Errorf("script quest set: %w", err)
	}
	return nil
}

func (a scriptQuestAdapter) GetVarOfNPC(charID uint32, npcName, varName string) int64 {
	v, err := a.svc.GetVarOfNPC(context.TODO(), charID, npcName, varName)
	if err != nil {
		return 0
	}
	return v
}
