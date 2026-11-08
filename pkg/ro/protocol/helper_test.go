//go:build unit

package protocol_test

import (
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

// installDefault installs the default protocol set for a test.
//
// The set is the Thai fork at the RE family: the fork is the primary
// target, and the RE family is the one both sources share.
//
// The test returns the set so a caller can compare against it.
func installDefault(t *testing.T) *protocol.Set {
	t.Helper()
	set, ok := protocol.Resolve(protocol.SourceThailand, protocol.VariantRE)
	if !ok {
		t.Fatal("the thailand source pack did not resolve")
	}
	protocol.Install(protocol.SourceThailand, protocol.VariantRE)
	return set
}
