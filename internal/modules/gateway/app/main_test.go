package app_test

import (
	"os"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

// TestMain installs the protocol set before the suite runs.
//
// The suite frames the server's packets, and the packets' opcodes and
// lengths come from the installed source pack, so the set has to be
// installed before any test frames one. The file carries no build tag:
// the suite runs under both the unit and the integration tag, and the
// set has to be installed under both.
func TestMain(m *testing.M) {
	protocol.Install(protocol.SourceThailand, protocol.VariantRE)
	os.Exit(m.Run())
}
