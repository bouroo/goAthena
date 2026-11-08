//go:build unit

package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/bouroo/goAthena/internal/app"
	"github.com/bouroo/goAthena/internal/modules/gateway"
	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
)

// remoteDirectory routes one map to a remote zone and everything else
// local (the zero Zone).
type remoteDirectory struct {
	zone transitdomain.Zone
}

func (d remoteDirectory) Resolve(mapName string) (transitdomain.Zone, error) {
	if mapName == "geffen" {
		return d.zone, nil
	}
	return transitdomain.Zone{}, nil
}

// failingDirectory fails every resolve.
type failingDirectory struct{}

func (failingDirectory) Resolve(string) (transitdomain.Zone, error) {
	return transitdomain.Zone{}, errors.New("directory unreachable")
}

func directoryLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestDirectoryMapEndpointsRoutesARemoteMap proves a map the directory
// resolves to another zone is answered with THAT zone's address.
//
// The client dials the address the char server hands it. A shard that
// answered with its own address would send the character to a shard that
// does not serve the map — the character would enter the wrong world.
func TestDirectoryMapEndpointsRoutesARemoteMap(t *testing.T) {
	// The zone's address is the source's field value: htonl of the
	// host-order IPv4, so 10.0.4.7 is 0x0a000407.
	resolver := &app.DirectoryMapEndpoints{
		Directory: remoteDirectory{zone: transitdomain.Zone{
			Name: "geffen",
			IPv4: 0x0a000407,
			Port: 5121,
		}},
		Local: gateway.MapEndpoint{Host: "127.0.0.1", Port: 5121},
		Log:   directoryLogger(),
	}

	endpoint, err := resolver.ResolveMapEndpointFor(context.Background(), "geffen")
	if err != nil {
		t.Fatalf("resolve geffen: %v", err)
	}
	if endpoint.Host != "10.0.4.7" {
		t.Errorf("remote host = %q, want 10.0.4.7", endpoint.Host)
	}
	if endpoint.Port != 5121 {
		t.Errorf("remote port = %d, want 5121", endpoint.Port)
	}
}

// TestDirectoryMapEndpointsKeepsALocalMapLocal proves a map the directory
// resolves locally is answered with THIS process's endpoint.
func TestDirectoryMapEndpointsKeepsALocalMapLocal(t *testing.T) {
	resolver := &app.DirectoryMapEndpoints{
		Directory: remoteDirectory{zone: transitdomain.Zone{IPv4: 0x0a000407, Port: 5121}},
		Local:     gateway.MapEndpoint{Host: "192.168.1.5", Port: 5121},
		Log:       directoryLogger(),
	}

	endpoint, err := resolver.ResolveMapEndpointFor(context.Background(), "prontera")
	if err != nil {
		t.Fatalf("resolve prontera: %v", err)
	}
	if endpoint.Host != "192.168.1.5" {
		t.Errorf("local host = %q, want 192.168.1.5", endpoint.Host)
	}
}

// TestDirectoryMapEndpointsFailsLoudly proves an unreachable directory is
// an error, not a silently-wrong address: the char server must keep the
// address it knows is right rather than hand out an unroutable one.
func TestDirectoryMapEndpointsFailsLoudly(t *testing.T) {
	resolver := &app.DirectoryMapEndpoints{
		Directory: failingDirectory{},
		Local:     gateway.MapEndpoint{Host: "127.0.0.1", Port: 5121},
		Log:       directoryLogger(),
	}

	if _, err := resolver.ResolveMapEndpointFor(context.Background(), "prontera"); err == nil {
		t.Error("resolve with a failing directory = nil error, want an error")
	}
}

// TestDirectoryMapEndpointsWithoutADirectory proves a nil directory is
// answered with the local endpoint: the single-process deployment has no
// directory to ask.
func TestDirectoryMapEndpointsWithoutADirectory(t *testing.T) {
	resolver := &app.DirectoryMapEndpoints{
		Local: gateway.MapEndpoint{Host: "127.0.0.1", Port: 5121},
		Log:   directoryLogger(),
	}

	endpoint, err := resolver.ResolveMapEndpointFor(context.Background(), "prontera")
	if err != nil {
		t.Fatalf("resolve prontera: %v", err)
	}
	if endpoint.Host != "127.0.0.1" || endpoint.Port != 5121 {
		t.Errorf("endpoint = %s:%d, want 127.0.0.1:5121", endpoint.Host, endpoint.Port)
	}
}
