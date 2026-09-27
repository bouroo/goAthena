//go:build unit

package staticdir

import (
	"errors"
	"testing"

	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
)

func TestNew_ParsesWireOrderZones(t *testing.T) {
	d, err := New(map[string]string{
		"geffen": "192.168.4.7:5121",
		"payon":  "10.0.0.1:7777",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	z, err := d.Resolve("geffen")
	if err != nil {
		t.Fatalf("resolve geffen: %v", err)
	}
	if got, want := z.IPv4, uint32(0xc0a80407); got != want {
		t.Errorf("ip = %#x, want %#x (big-endian 192.168.4.7)", got, want)
	}
	if got, want := z.Port, uint16(0x1401); got != want {
		t.Errorf("port = %#x, want %#x (5121)", got, want)
	}
	if z2, _ := d.Resolve("unrouted-map"); z2 != (transitdomain.Zone{}) {
		t.Errorf("unrouted map should resolve local zero Zone, got %+v", z2)
	}
}

func TestNew_RejectsMalformedRoutes(t *testing.T) {
	for name, routes := range map[string]map[string]string{
		"hostname": {"geffen": "zone-b.example.com:5121"},
		"noport":   {"geffen": "10.0.0.9"},
		"ipv6":     {"geffen": "[::1]:5121"},
	} {
		if _, err := New(routes); !errors.Is(err, err) || err == nil {
			t.Errorf("%s: want error, got %v", name, err)
		}
	}
}
