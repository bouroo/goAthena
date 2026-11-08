package packet

import (
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

// TestMain installs the protocol set the codecs read.
//
// The set is installed once for the package, because the opcodes and the
// frame lengths are resolved from it. It is the Thai fork at the RE family:
// the fork is the primary target, and the RE family is the one both source
// packs share.
func TestMain(m *testing.M) {
	protocol.Install(protocol.SourceThailand, protocol.VariantRE)
	m.Run()
}
