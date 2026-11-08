// Package remote carries the economy Service across the NATS bus: the Proxy
// is the client-side port a zone process resolves, the Server is the
// request/reply adapter a `goathena serve-economy` host runs against the
// in-process service. JSON payloads keep the wire debuggable with `nats sub`;
// if a hop ever needs the bytes, msgpack is a swap behind these structs.
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
)

// Subject taxonomy: goathena.<module>.v<wire>.<verb>. The v0 suffix is the
// wire-format version — a field added to a request struct is a v0-compatible
// change, a field removed or re-typed bumps the verb to v1, letting old and
// new hosts run side by side during a rolling deploy.
const (
	SubjectV0         = "goathena.economy.v0"
	SubjectZenyGet    = SubjectV0 + ".zeny.get"
	SubjectZenyCredit = SubjectV0 + ".zeny.credit"
	SubjectZenyDeduct = SubjectV0 + ".zeny.deduct"
)

// QueueGroup load-balances requests across economy-host replicas: every
// replica subscribes with this group and the bus delivers each request to
// exactly one of them.
const QueueGroup = "economy"

// DefaultTimeout bounds one proxy call when the caller's context carries no
// deadline. Matches the config knob (config.NATSConfig.RequestTimeout).
const DefaultTimeout = 5 * time.Second

// zenyGetReq asks for a character's balance.
type zenyGetReq struct {
	CharID uint32 `json:"char_id"`
}

// zenyMoveReq is one credit or deduct. Entry carries the ledger-audit fields
// the caller supplied (or would have supplied) in-process; the server writes
// them verbatim so a remote row is byte-identical to the local one.
type zenyMoveReq struct {
	CharID uint32          `json:"char_id"`
	Amount int32           `json:"amount"`
	Entry  ledgerEntryJSON `json:"entry"`
}

// ledgerEntryJSON mirrors app.LedgerEntry on the wire. Reason is the
// persisted string form of economydomain.Reason (stable across releases).
type ledgerEntryJSON struct {
	Reason     string `json:"reason,omitempty"`
	PeerCharID uint32 `json:"peer_char_id,omitempty"`
	MapName    string `json:"map_name,omitempty"`
}

func entryToWire(e app.LedgerEntry) ledgerEntryJSON {
	return ledgerEntryJSON{Reason: string(e.Reason), PeerCharID: e.PeerCharID, MapName: e.MapName}
}

func (w ledgerEntryJSON) toEntry() app.LedgerEntry {
	return app.LedgerEntry{Reason: economydomain.Reason(w.Reason), PeerCharID: w.PeerCharID, MapName: w.MapName}
}

// reply is the single envelope every verb answers with. Exactly one of
// Amount (get) or ErrCode is meaningful; ok=false always pairs with ErrCode.
type reply struct {
	OK      bool   `json:"ok"`
	Amount  int32  `json:"amount,omitempty"`
	ErrCode string `json:"err_code,omitempty"`
	ErrMsg  string `json:"err_msg,omitempty"`
}

// Error codes. The well-known sentinels ride the wire as codes so a proxy
// caller can errors.Is against the same sentinel a local caller would have
// received; anything else degrades to codeInternal.
const (
	codeInsufficientFunds = "insufficient_funds"
	codeOverflow          = "zeny_overflow"
	codeCharNotFound      = "character_not_found"
	codeLedgerAppend      = "ledger_append_failed"
	codeInternal          = "internal"
)

// codeFor classifies err for the wire. The sentinel set is exactly what the
// seven service verbs can return: insufficient funds / overflow from the
// domain math, character-not-found from the char lookup, ledger refusal from
// the audit gate, and everything else (driver, context) is internal.
func codeFor(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, economydomain.ErrInsufficientFunds):
		return codeInsufficientFunds
	case errors.Is(err, economydomain.ErrOverflow):
		return codeOverflow
	case errors.Is(err, chardomain.ErrCharacterNotFound):
		return codeCharNotFound
	case errors.Is(err, app.ErrLedgerAppendFailed):
		return codeLedgerAppend
	default:
		return codeInternal
	}
}

// errFor rebuilds the sentinel from a wire code. Known codes return the
// canonical sentinel — errors.Is behaves exactly as in-process, and no host
// detail (driver text) crosses the bus. Unknown codes (a newer host answering
// an older proxy) keep the message and stay non-sentinel.
func errFor(code, msg string) error {
	switch code {
	case "":
		return nil
	case codeInsufficientFunds:
		return economydomain.ErrInsufficientFunds
	case codeOverflow:
		return economydomain.ErrOverflow
	case codeCharNotFound:
		return chardomain.ErrCharacterNotFound
	case codeLedgerAppend:
		return app.ErrLedgerAppendFailed
	default:
		return fmt.Errorf("%s", msg)
	}
}

// encodeReply marshals the envelope. A marshal failure cannot reach the
// client (int32/bool/string only), so it degrades to the codeInternal reply
// built here.
func encodeReply(r reply) []byte {
	data, err := json.Marshal(r)
	if err != nil {
		return []byte(`{"ok":false,"err_code":"internal","err_msg":"reply encode failed"}`)
	}
	return data
}

func decodeRequest[T any](data []byte) (T, error) {
	var req T
	if err := json.Unmarshal(data, &req); err != nil {
		return req, fmt.Errorf("decode request: %w", err)
	}
	return req, nil
}
