//go:build unit

package identityv1_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	gatewayv1 "github.com/bouroo/goAthena/api/pb/gateway/v1"
	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
)

func TestAuthenticateRequestRoundTrip(t *testing.T) {
	t.Parallel()

	in := &identityv1.AuthenticateRequest{
		Username:   "alice",
		Password:   []byte("hunter2"),
		ClientType: 0x0c,
		Packetver:  20130807,
		ClientIp:   "203.0.113.5",
		Method:     identityv1.AuthMethod_AUTH_METHOD_MD5_SALTED,
		Md5Salt:    []byte("0123456789abcdef"),
	}

	data, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out := &identityv1.AuthenticateRequest{}
	if err := proto.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !proto.Equal(in, out) {
		t.Fatalf("round-trip mismatch:\n in:  %+v\n out: %+v", in, out)
	}
}

func TestAuthenticateResponseRoundTrip(t *testing.T) {
	t.Parallel()

	in := &identityv1.AuthenticateResponse{
		Result:    identityv1.AuthResult_AUTH_RESULT_OK,
		ErrorCode: 0,
		AccountId: 2000001,
		LoginId1:  0x0123456789abcdef,
		LoginId2:  0xfedcba9876543210,
		LastIp:    "198.51.100.42",
		LastLogin: "2026-07-05 14:00:00",
		Sex:       "M",
		Token:     "tok-abc123",
		CharServers: []*identityv1.CharServerInfo{
			{Ip: "10.0.0.10", Port: 6121, Name: "Aurora", Users: 42, ServerType: 0, NewDisplay: "0"},
			{Ip: "10.0.0.11", Port: 6122, Name: "Boreas", Users: 7, ServerType: 0, NewDisplay: "0"},
		},
	}

	data, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out := &identityv1.AuthenticateResponse{}
	if err := proto.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !proto.Equal(in, out) {
		t.Fatalf("round-trip mismatch:\n in:  %+v\n out: %+v", in, out)
	}
	if got := len(out.GetCharServers()); got != 2 {
		t.Fatalf("char_servers len = %d, want 2", got)
	}
}

func TestEnterZoneRequestRoundTrip(t *testing.T) {
	t.Parallel()

	in := &zonev1.EnterZoneRequest{
		AccountId:  2000001,
		CharId:     150001,
		LoginId1:   0x0123456789abcdef,
		ClientTick: 12345678,
		Sex:        "F",
		Packetver:  20130807,
		ClientIp:   "203.0.113.5",
	}

	data, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out := &zonev1.EnterZoneRequest{}
	if err := proto.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !proto.Equal(in, out) {
		t.Fatalf("round-trip mismatch:\n in:  %+v\n out: %+v", in, out)
	}
}

func TestGetStatusResponseRoundTrip(t *testing.T) {
	t.Parallel()

	in := &gatewayv1.GetStatusResponse{
		ActiveConnections: 17,
		PacketsReceived:   1_234_567,
		PacketsForwarded:  1_200_000,
		DecryptErrors:     3,
		Version:           "0.1.0",
	}

	data, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out := &gatewayv1.GetStatusResponse{}
	if err := proto.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !proto.Equal(in, out) {
		t.Fatalf("round-trip mismatch:\n in:  %+v\n out: %+v", in, out)
	}
}
