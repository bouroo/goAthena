package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/chat/domain"
)

type grpcHandler struct {
	zonev1.UnimplementedZoneServiceServer
	svc    domain.ChatService
	logger *zerolog.Logger
}

// NewGRPCHandler creates a new chat gRPC handler.
func NewGRPCHandler(svc domain.ChatService, logger *zerolog.Logger) zonev1.ZoneServiceServer {
	return &grpcHandler{svc: svc, logger: logger}
}

func (h *grpcHandler) Whisper(ctx context.Context, req *zonev1.WhisperRequest) (*zonev1.WhisperResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetSenderCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender_char_id must be > 0")
	}
	if req.GetTargetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "target_char_id must be > 0")
	}
	if req.GetContent() == "" {
		return nil, status.Error(codes.InvalidArgument, "content must not be empty")
	}

	h.logger.Debug().
		Uint32("sender_char_id", req.GetSenderCharId()).
		Uint32("target_char_id", req.GetTargetCharId()).
		Msg("chat: Whisper called")

	if err := h.svc.Whisper(ctx, req.GetSenderCharId(), req.GetTargetCharId(), req.GetContent()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("sender_char_id", req.GetSenderCharId()).
			Uint32("target_char_id", req.GetTargetCharId()).
			Msg("chat: Whisper failed")

		return &zonev1.WhisperResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("sender_char_id", req.GetSenderCharId()).
		Uint32("target_char_id", req.GetTargetCharId()).
		Msg("chat: Whisper processed")

	return &zonev1.WhisperResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) SendPartyChat(ctx context.Context, req *zonev1.SendPartyChatRequest) (*zonev1.SendPartyChatResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetSenderCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender_char_id must be > 0")
	}
	if req.GetContent() == "" {
		return nil, status.Error(codes.InvalidArgument, "content must not be empty")
	}

	h.logger.Debug().
		Uint32("sender_char_id", req.GetSenderCharId()).
		Msg("chat: SendPartyChat called")

	if err := h.svc.SendPartyChat(ctx, req.GetSenderCharId(), req.GetContent()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("sender_char_id", req.GetSenderCharId()).
			Msg("chat: SendPartyChat failed")

		return &zonev1.SendPartyChatResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("sender_char_id", req.GetSenderCharId()).
		Msg("chat: SendPartyChat processed")

	return &zonev1.SendPartyChatResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) SendMapChat(ctx context.Context, req *zonev1.SendMapChatRequest) (*zonev1.SendMapChatResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetSenderCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender_char_id must be > 0")
	}
	if req.GetMapName() == "" {
		return nil, status.Error(codes.InvalidArgument, "map_name must not be empty")
	}
	if req.GetContent() == "" {
		return nil, status.Error(codes.InvalidArgument, "content must not be empty")
	}

	h.logger.Debug().
		Uint32("sender_char_id", req.GetSenderCharId()).
		Str("map_name", req.GetMapName()).
		Msg("chat: SendMapChat called")

	if err := h.svc.SendMapChat(ctx, req.GetSenderCharId(), req.GetMapName(), req.GetContent()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("sender_char_id", req.GetSenderCharId()).
			Str("map_name", req.GetMapName()).
			Msg("chat: SendMapChat failed")

		return &zonev1.SendMapChatResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("sender_char_id", req.GetSenderCharId()).
		Str("map_name", req.GetMapName()).
		Msg("chat: SendMapChat processed")

	return &zonev1.SendMapChatResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) SendFriendRequest(ctx context.Context, req *zonev1.SendFriendRequestRequest) (*zonev1.SendFriendRequestResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetFromAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "from_account_id must be > 0")
	}
	if req.GetToAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "to_account_id must be > 0")
	}

	h.logger.Debug().
		Uint32("from_account_id", req.GetFromAccountId()).
		Uint32("to_account_id", req.GetToAccountId()).
		Msg("chat: SendFriendRequest called")

	if err := h.svc.SendFriendRequest(ctx, req.GetFromAccountId(), req.GetToAccountId(), req.GetFromName(), req.GetToName()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("from_account_id", req.GetFromAccountId()).
			Uint32("to_account_id", req.GetToAccountId()).
			Msg("chat: SendFriendRequest failed")

		return &zonev1.SendFriendRequestResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
			RequestId:    0,
		}, nil
	}

	h.logger.Debug().
		Uint32("from_account_id", req.GetFromAccountId()).
		Uint32("to_account_id", req.GetToAccountId()).
		Msg("chat: SendFriendRequest processed")

	return &zonev1.SendFriendRequestResponse{
		Success:      true,
		ErrorMessage: "",
		RequestId:    0,
	}, nil
}

func (h *grpcHandler) AcceptFriendRequest(ctx context.Context, req *zonev1.AcceptFriendRequestRequest) (*zonev1.AcceptFriendRequestResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetRequestId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "request_id must be > 0")
	}

	h.logger.Debug().
		Uint64("request_id", req.GetRequestId()).
		Msg("chat: AcceptFriendRequest called")

	if err := h.svc.AcceptFriendRequest(ctx, req.GetRequestId()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint64("request_id", req.GetRequestId()).
			Msg("chat: AcceptFriendRequest failed")

		return &zonev1.AcceptFriendRequestResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Uint64("request_id", req.GetRequestId()).
		Msg("chat: AcceptFriendRequest processed")

	return &zonev1.AcceptFriendRequestResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) RejectFriendRequest(ctx context.Context, req *zonev1.RejectFriendRequestRequest) (*zonev1.RejectFriendRequestResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetRequestId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "request_id must be > 0")
	}

	h.logger.Debug().
		Uint64("request_id", req.GetRequestId()).
		Msg("chat: RejectFriendRequest called")

	if err := h.svc.RejectFriendRequest(ctx, req.GetRequestId()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint64("request_id", req.GetRequestId()).
			Msg("chat: RejectFriendRequest failed")

		return &zonev1.RejectFriendRequestResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Uint64("request_id", req.GetRequestId()).
		Msg("chat: RejectFriendRequest processed")

	return &zonev1.RejectFriendRequestResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) RemoveFriend(ctx context.Context, req *zonev1.RemoveFriendRequest) (*zonev1.RemoveFriendResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id must be > 0")
	}
	if req.GetFriendAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "friend_account_id must be > 0")
	}

	h.logger.Debug().
		Uint32("account_id", req.GetAccountId()).
		Uint32("friend_account_id", req.GetFriendAccountId()).
		Msg("chat: RemoveFriend called")

	if err := h.svc.RemoveFriend(ctx, req.GetAccountId(), req.GetFriendAccountId()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("account_id", req.GetAccountId()).
			Uint32("friend_account_id", req.GetFriendAccountId()).
			Msg("chat: RemoveFriend failed")

		return &zonev1.RemoveFriendResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("account_id", req.GetAccountId()).
		Uint32("friend_account_id", req.GetFriendAccountId()).
		Msg("chat: RemoveFriend processed")

	return &zonev1.RemoveFriendResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) ListFriends(ctx context.Context, req *zonev1.ListFriendsRequest) (*zonev1.ListFriendsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id must be > 0")
	}

	h.logger.Debug().
		Uint32("account_id", req.GetAccountId()).
		Msg("chat: ListFriends called")

	friends, err := h.svc.ListFriends(ctx, req.GetAccountId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("account_id", req.GetAccountId()).
			Msg("chat: ListFriends failed")

		return &zonev1.ListFriendsResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
			Friends:      []*zonev1.FriendInfo{},
		}, nil
	}

	h.logger.Debug().
		Uint32("account_id", req.GetAccountId()).
		Int("friend_count", len(friends)).
		Msg("chat: ListFriends processed")

	friendInfos := make([]*zonev1.FriendInfo, len(friends))
	for i, f := range friends {
		friendInfos[i] = toProtoFriendInfo(f)
	}

	return &zonev1.ListFriendsResponse{
		Success:      true,
		ErrorMessage: "",
		Friends:      friendInfos,
	}, nil
}

func (h *grpcHandler) CreateParty(ctx context.Context, req *zonev1.CreatePartyRequest) (*zonev1.CreatePartyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name must not be empty")
	}
	if req.GetLeaderCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "leader_char_id must be > 0")
	}

	h.logger.Debug().
		Str("name", req.GetName()).
		Uint32("leader_char_id", req.GetLeaderCharId()).
		Msg("chat: CreateParty called")

	party, err := h.svc.CreateParty(ctx, req.GetName(), req.GetLeaderCharId(), req.GetLeaderName())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("name", req.GetName()).
			Uint32("leader_char_id", req.GetLeaderCharId()).
			Msg("chat: CreateParty failed")

		return &zonev1.CreatePartyResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
			PartyId:      "",
		}, nil
	}

	h.logger.Debug().
		Str("party_id", party.ID).
		Str("name", req.GetName()).
		Uint32("leader_char_id", req.GetLeaderCharId()).
		Msg("chat: CreateParty processed")

	return &zonev1.CreatePartyResponse{
		Success:      true,
		ErrorMessage: "",
		PartyId:      party.ID,
	}, nil
}

func (h *grpcHandler) JoinParty(ctx context.Context, req *zonev1.JoinPartyRequest) (*zonev1.JoinPartyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetPartyId() == "" {
		return nil, status.Error(codes.InvalidArgument, "party_id must not be empty")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	h.logger.Debug().
		Str("party_id", req.GetPartyId()).
		Uint32("char_id", req.GetCharId()).
		Msg("chat: JoinParty called")

	if err := h.svc.JoinParty(ctx, req.GetPartyId(), req.GetCharId(), req.GetCharName()); err != nil {
		h.logger.Error().Stack().Err(err).
			Str("party_id", req.GetPartyId()).
			Uint32("char_id", req.GetCharId()).
			Msg("chat: JoinParty failed")

		return &zonev1.JoinPartyResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Str("party_id", req.GetPartyId()).
		Uint32("char_id", req.GetCharId()).
		Msg("chat: JoinParty processed")

	return &zonev1.JoinPartyResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) LeaveParty(ctx context.Context, req *zonev1.LeavePartyRequest) (*zonev1.LeavePartyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetPartyId() == "" {
		return nil, status.Error(codes.InvalidArgument, "party_id must not be empty")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	h.logger.Debug().
		Str("party_id", req.GetPartyId()).
		Uint32("char_id", req.GetCharId()).
		Msg("chat: LeaveParty called")

	if err := h.svc.LeaveParty(ctx, req.GetPartyId(), req.GetCharId()); err != nil {
		h.logger.Error().Stack().Err(err).
			Str("party_id", req.GetPartyId()).
			Uint32("char_id", req.GetCharId()).
			Msg("chat: LeaveParty failed")

		return &zonev1.LeavePartyResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
		}, nil
	}

	h.logger.Debug().
		Str("party_id", req.GetPartyId()).
		Uint32("char_id", req.GetCharId()).
		Msg("chat: LeaveParty processed")

	return &zonev1.LeavePartyResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func (h *grpcHandler) GetParty(ctx context.Context, req *zonev1.GetPartyRequest) (*zonev1.GetPartyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetPartyId() == "" {
		return nil, status.Error(codes.InvalidArgument, "party_id must not be empty")
	}

	h.logger.Debug().
		Str("party_id", req.GetPartyId()).
		Msg("chat: GetParty called")

	party, err := h.svc.GetParty(ctx, req.GetPartyId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("party_id", req.GetPartyId()).
			Msg("chat: GetParty failed")

		return &zonev1.GetPartyResponse{
			Success:      false,
			ErrorMessage: mapChatError(err),
			Party:        nil,
		}, nil
	}

	h.logger.Debug().
		Str("party_id", req.GetPartyId()).
		Msg("chat: GetParty processed")

	return &zonev1.GetPartyResponse{
		Success:      true,
		ErrorMessage: "",
		Party:        toProtoPartyInfo(party),
	}, nil
}

func mapChatError(err error) string {
	switch {
	case errors.Is(err, domain.ErrPlayerNotFound):
		return "Player not found"
	case errors.Is(err, domain.ErrNotFriends):
		return "Not friends"
	case errors.Is(err, domain.ErrFriendAlreadyExists):
		return "Already friends"
	case errors.Is(err, domain.ErrFriendRequestPending):
		return "Friend request already pending"
	case errors.Is(err, domain.ErrPartyNotFound):
		return "Party not found"
	case errors.Is(err, domain.ErrPartyFull):
		return "Party is full"
	case errors.Is(err, domain.ErrNotPartyMember):
		return "Not a party member"
	case errors.Is(err, domain.ErrAlreadyInParty):
		return "Already in a party"
	case errors.Is(err, domain.ErrEmptyMessage):
		return "Message is empty"
	case errors.Is(err, domain.ErrMessageTooLong):
		return "Message is too long"
	default:
		return fmt.Sprintf("Chat error: %v", err)
	}
}

func toProtoFriendInfo(f domain.Friendship) *zonev1.FriendInfo {
	return &zonev1.FriendInfo{
		AccountId:       f.AccountID,
		FriendAccountId: f.FriendAccountID,
		FriendName:      f.FriendName,
		Online:          f.Status == domain.FriendStatusOnline,
	}
}

func toProtoPartyInfo(p domain.Party) *zonev1.PartyInfo {
	members := make([]*zonev1.PartyMemberInfo, len(p.Members))
	for i, m := range p.Members {
		members[i] = &zonev1.PartyMemberInfo{
			CharId:   m.CharID,
			Name:     m.Name,
			IsLeader: m.IsLeader,
		}
	}
	return &zonev1.PartyInfo{
		Id:           p.ID,
		Name:         p.Name,
		LeaderCharId: p.LeaderID,
		Members:      members,
	}
}
