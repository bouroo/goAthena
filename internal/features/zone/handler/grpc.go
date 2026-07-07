// Package handler contains the transport-layer adapter for the zone
// feature (WS-C): the gRPC server that implements zonev1.ZoneService
// and is invoked by the gateway when a client enters the map server.
package handler

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
)

// grpcHandler implements zonev1.ZoneServiceServer. It is a thin adapter:
// proto <-> domain mapping, request validation, and error translation.
type grpcHandler struct {
	zonev1.UnimplementedZoneServiceServer
	svc     domain.ZoneService
	mapName string
	spawnX  int
	spawnY  int
	logger  *zerolog.Logger
}

// NewGRPCHandler creates a gRPC handler for the ZoneService. The returned
// value is registered onto a *grpc.Server by the zone DI package via
// zonev1.RegisterZoneServiceServer.
func NewGRPCHandler(
	svc domain.ZoneService,
	mapName string,
	spawnX, spawnY int,
	logger *zerolog.Logger,
) zonev1.ZoneServiceServer {
	return &grpcHandler{
		svc:     svc,
		mapName: mapName,
		spawnX:  spawnX,
		spawnY:  spawnY,
		logger:  logger,
	}
}

// EnterZone handles CZ_ENTER (0x0072 / WantToConnection) packets forwarded
// from the gateway as structured gRPC. It validates the request, registers
// the player in the zone's tick loop and AOI grid, and returns the spawn
// map + coordinates on success.
//
// Like identity.Authenticate, an AddEntity failure is a wire outcome
// carried inside EnterZoneResponse (Success=false, Error=...) rather than a
// gRPC status error: a rejected map entry is a normal flow outcome for the
// caller, not a server-side fault.
func (h *grpcHandler) EnterZone(
	ctx context.Context,
	req *zonev1.EnterZoneRequest,
) (*zonev1.EnterZoneResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id must be > 0")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	entity := domain.Entity{
		ID:   domain.EntityID(req.GetAccountId()), //nolint:gosec // account_id is validated > 0 and fits uint32 range
		Type: domain.EntityPlayer,
		X:    h.spawnX,
		Y:    h.spawnY,
	}

	if err := h.svc.AddEntity(ctx, &entity); err != nil {
		h.logger.Warn().
			Err(err).
			Uint32("account_id", req.GetAccountId()).
			Uint32("char_id", req.GetCharId()).
			Str("map", h.mapName).
			Msg("zone: EnterZone AddEntity failed")
		return &zonev1.EnterZoneResponse{
			Success: false,
			Error:   fmt.Sprintf("zone entry failed: %v", err),
		}, nil
	}

	h.logger.Info().
		Uint32("account_id", req.GetAccountId()).
		Uint32("char_id", req.GetCharId()).
		Str("map", h.mapName).
		Int("x", h.spawnX).
		Int("y", h.spawnY).
		Msg("zone: player entered")

	return &zonev1.EnterZoneResponse{
		Success: true,
		MapName: h.mapName,
		MapX:    uint32(h.spawnX), //nolint:gosec // spawn coords are map cell positions (0-512)
		MapY:    uint32(h.spawnY), //nolint:gosec // spawn coords are map cell positions (0-512)
	}, nil
}
