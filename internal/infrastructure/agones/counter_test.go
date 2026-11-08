package agones

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"agones.dev/agones/pkg/sdk/beta"
)

// fakeBeta records the counter updates it receives.
type fakeBeta struct {
	updates []*beta.UpdateCounterRequest
	err     error
}

func (f *fakeBeta) UpdateCounter(_ context.Context, in *beta.UpdateCounterRequest, _ ...grpc.CallOption) (*beta.Counter, error) {
	f.updates = append(f.updates, in)
	if f.err != nil {
		return nil, f.err
	}
	return &beta.Counter{Name: in.CounterUpdateRequest.GetName()}, nil
}

// TestReportCounterSendsTheCountAndCapacity proves the shard's report
// carries both the count and the capacity — Agones rejects a count
// outside [0, capacity], and the capacity is what the autoscaler divides
// the fleet's aggregate by.
func TestReportCounterSendsTheCountAndCapacity(t *testing.T) {
	f := &fakeBeta{}
	s := &Sidecar{beta: f}

	if err := s.ReportCounter(context.Background(), PlayersCounterName, 42, PlayerCounterCapacity); err != nil {
		t.Fatalf("ReportCounter: %v", err)
	}
	if len(f.updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(f.updates))
	}
	req := f.updates[0].CounterUpdateRequest
	if req.GetName() != PlayersCounterName {
		t.Errorf("counter name = %q, want %q", req.GetName(), PlayersCounterName)
	}
	if got := req.GetCount().GetValue(); got != 42 {
		t.Errorf("count = %d, want 42", got)
	}
	if got := req.GetCapacity().GetValue(); got != PlayerCounterCapacity {
		t.Errorf("capacity = %d, want %d", got, PlayerCounterCapacity)
	}
}

// TestReportCounterRejectsAnImpossibleCount proves a count the sidecar
// would reject is refused here, with a message that says why.
//
// The sidecar answers OUT_OF_RANGE for a count above the capacity, and
// answers nothing for a negative one. Catching both locally means the
// error names the cause instead of surfacing as a gRPC status.
func TestReportCounterRejectsAnImpossibleCount(t *testing.T) {
	cases := []struct {
		name      string
		count     int64
		capacity  int64
		wantError string
	}{
		{"negative count", -1, 100, "negative"},
		{"negative capacity", 1, -1, "negative"},
		{"count above capacity", 101, 100, "exceeds capacity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBeta{}
			s := &Sidecar{beta: f}
			err := s.ReportCounter(context.Background(), PlayersCounterName, tc.count, tc.capacity)
			if err == nil {
				t.Fatal("ReportCounter = nil error, want a rejection")
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantError)
			}
			if len(f.updates) != 0 {
				t.Errorf("the sidecar was called %d times, want 0 (the count is refused locally)", len(f.updates))
			}
		})
	}
}

// TestReportCounterPropagatesTheSidecarError proves a sidecar failure
// is reported rather than swallowed: the count would otherwise go stale
// and the autoscaler would size the fleet on an old number.
func TestReportCounterPropagatesTheSidecarError(t *testing.T) {
	s := &Sidecar{beta: &fakeBeta{err: context.DeadlineExceeded}}
	err := s.ReportCounter(context.Background(), PlayersCounterName, 1, 100)
	if err == nil {
		t.Fatal("ReportCounter = nil error, want the sidecar's error")
	}
	if !strings.Contains(err.Error(), "update counter") {
		t.Errorf("error = %q, want it to name the failing call", err)
	}
}

// TestNoopHasNoCounter proves the no-op counter reporter is inert: a
// process that does not run as a GameServer has nothing to publish.
func TestNoopHasNoCounter(t *testing.T) {
	var p Noop
	if err := p.ReportCounter(context.Background(), PlayersCounterName, 1, 100); err != nil {
		t.Fatalf("Noop.ReportCounter: %v", err)
	}
}
