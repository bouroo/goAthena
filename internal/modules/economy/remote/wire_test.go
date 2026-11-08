package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
)

func TestCodeForErrForRoundTrip(t *testing.T) {
	sentinels := map[string]error{
		codeInsufficientFunds: economydomain.ErrInsufficientFunds,
		codeOverflow:          economydomain.ErrOverflow,
		codeCharNotFound:      chardomain.ErrCharacterNotFound,
		codeLedgerAppend:      app.ErrLedgerAppendFailed,
	}
	for code, sentinel := range sentinels {
		wire := fmt.Errorf("server-side detail: %w", sentinel)
		if got := codeFor(wire); got != code {
			t.Errorf("codeFor(%v) = %q, want %q", wire, got, code)
		}
		back := errFor(code, "server-side detail")
		if !errors.Is(back, sentinel) {
			t.Errorf("errFor(%q) does not match sentinel %v", code, sentinel)
		}
	}
	if got := codeFor(errors.New("mystery")); got != codeInternal {
		t.Errorf("codeFor(unknown) = %q, want %q", got, codeInternal)
	}
	if got := codeFor(nil); got != "" {
		t.Errorf("codeFor(nil) = %q, want empty", got)
	}
}

func TestErrForUnknownCodeKeepsMessage(t *testing.T) {
	err := errFor("some_future_code", "host said no")
	if err == nil || !strings.Contains(err.Error(), "host said no") {
		t.Fatalf("errFor(future code) = %v, want message preserved", err)
	}
	if errors.Is(err, economydomain.ErrInsufficientFunds) {
		t.Error("unknown code must not match a sentinel")
	}
}

func TestErrForKnownCodeIsBareSentinel(t *testing.T) {
	// Known codes carry no host detail: the canonical sentinel text is the
	// whole message, and driver errors stay in the host log.
	err := errFor(codeLedgerAppend, "host detail that must not appear")
	if !errors.Is(err, app.ErrLedgerAppendFailed) {
		t.Fatalf("errFor = %v, want ledger sentinel", err)
	}
	if strings.Contains(err.Error(), "host detail") {
		t.Errorf("known-code error leaked host detail: %v", err)
	}
}

func TestLedgerEntryWireRoundTrip(t *testing.T) {
	in := app.LedgerEntry{Reason: economydomain.ReasonTrade, PeerCharID: 42, MapName: "prontera"}
	if got := entryToWire(in).toEntry(); got != in {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}
	// An empty entry survives as the zero value, which the service's ledger
	// append degrades to ReasonUnknown — identical to a local plain call.
	if got := entryToWire(app.LedgerEntry{}).toEntry(); got != (app.LedgerEntry{}) {
		t.Errorf("empty entry round trip = %+v, want zero", got)
	}
}

func TestEncodeReplyAlwaysValidJSON(t *testing.T) {
	var seen reply
	if err := json.Unmarshal(encodeReply(reply{OK: true, Amount: 7}), &seen); err != nil {
		t.Fatalf("ok reply not valid JSON: %v", err)
	}
	if seen.Amount != 7 {
		t.Errorf("amount = %d, want 7", seen.Amount)
	}
}

func TestDecodeRequestRejectsGarbage(t *testing.T) {
	if _, err := decodeRequest[zenyGetReq]([]byte("{not json")); err == nil {
		t.Fatal("decodeRequest accepted malformed JSON")
	}
}
