package batch

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

const PoolWorkflowName = "HLLMCPAPoolV2"
const maximumStartIntents = 64
const pendingClaimLimit = 32
const maximumPoolSnapshot = 256 << 10

type PoolPermit struct {
	ID           string
	Revision     int64
	Applied      bool
	AwaitingStop bool
	Claimed      bool `json:",omitempty"`
}
type PoolState struct {
	PoolID                       string
	Generation                   int64
	Controller                   Controller
	Sequence                     int64
	PolicyRevision               int64
	Episode                      int64
	WindowID                     int64
	WindowAnchored               bool
	AfterScope                   string
	Outstanding                  []PoolPermit
	StartIntents                 []Reservation
	ProbeID                      string
	ProbeClaimAt                 time.Time
	DispatchNeedsReady           bool
	DispatchBlockedUntil         time.Time
	DispatchBackoff              time.Duration
	Quiescing                    bool
	PersistenceWait              bool
	PublicationAfter             int64
	PublicationAt                time.Time
	PublicationReason            string
	PublicationNext              *time.Time
	RequestRetirements           []string `json:",omitempty"`
	CoalesceDispatchFailures     bool     `json:",omitempty"`
	PendingClaimLimit            int      `json:",omitempty"`
	CoalesceTransientPublication bool     `json:",omitempty"`
}

func NewPoolState(pool string, generation int64, policy Policy) PoolState {
	return PoolState{PoolID: pool, Generation: generation, Controller: NewController(policy), PolicyRevision: 1, Episode: 1, WindowID: 1, DispatchBackoff: 2 * time.Second, CoalesceDispatchFailures: true, PendingClaimLimit: pendingClaimLimit, CoalesceTransientPublication: true}
}

func (s PoolState) pendingClaimsFull() bool {
	if s.PendingClaimLimit == 0 {
		return false
	} // Retained historical behavior.
	unclaimed := 0
	for _, p := range s.Outstanding {
		if !p.Claimed {
			unclaimed++
		}
	}
	return unclaimed >= s.PendingClaimLimit
}
func (s *PoolState) refreshWindow(now time.Time) {
	if s.WindowAnchored && s.Controller.Policy.WindowRequests > 0 && !now.Before(s.Controller.WindowStart.Add(s.Controller.Policy.WindowDuration)) {
		s.WindowID++
		s.WindowAnchored = false
		s.Controller.WindowStart = time.Time{}
		s.Controller.WindowStarts = 0
		if !s.Controller.Cooling && s.Controller.RefillWindowBurst {
			s.Controller.BurstLeft = s.Controller.Policy.Burst
		}
	}
}
func (s *PoolState) unresolved() bool {
	for _, p := range s.Outstanding {
		if p.AwaitingStop {
			return true
		}
	}
	return false
}
func (s *PoolState) CanGrant(now time.Time) bool {
	s.refreshWindow(now)
	if s.Quiescing || s.PersistenceWait || s.DispatchNeedsReady || s.unresolved() || s.pendingClaimsFull() || len(s.StartIntents) >= maximumStartIntents || len(s.RequestRetirements)+len(s.Outstanding) >= 1000 {
		return false
	}
	c := &s.Controller
	if !c.Cooling && !s.WindowAnchored && c.Policy.WindowRequests > 0 && c.WindowStarts >= c.Policy.WindowRequests {
		return false
	}
	return c.CanStart(now, len(s.Outstanding))
}
func (s *PoolState) Grant(now time.Time, item ReadyItem) (Reservation, error) {
	kind := item.Kind
	if kind == "" {
		kind = "job"
	}
	validOwner := (kind == "job" && validPermitID(item.Ref.JobID) && item.Ref.Index >= 0 && item.Ref.Index < MaximumItems && item.RequestID == "") || (kind == "ordinary" && validPermitID(item.RequestID) && item.Ref.JobID == "" && item.Ref.Index == 0)
	if !s.CanGrant(now) || !validPool(s.PoolID) || s.Generation < 1 || s.Sequence == math.MaxInt64 || !validScope(item.Ref.Scope) || !validOwner || item.BudgetMS < 1 || item.BudgetMS > MaximumGenerationBudget.Milliseconds() || !now.Before(item.Deadline) {
		return Reservation{}, ErrUnavailable
	}
	for _, r := range s.StartIntents {
		if r.Kind == kind && ((kind == "job" && r.Ref == item.Ref) || (kind == "ordinary" && r.RequestID == item.RequestID)) {
			return Reservation{}, ErrConflict
		}
	}
	s.Sequence++
	identity := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s\x00%d", s.PoolID, s.Generation, s.Sequence, item.Ref.Scope, item.Ref.JobID, item.Ref.Index)
	if kind == "ordinary" {
		identity += "\x00ordinary\x00" + item.RequestID
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String()
	r := Reservation{PermitID: id, PoolID: s.PoolID, Generation: s.Generation, Sequence: s.Sequence, Ref: item.Ref, Kind: "job", WorkflowID: "hllm-cpa-attempt-v2/" + id, GrantedAt: now, StartBy: minTime(now.Add(5*time.Second), item.Deadline), Deadline: item.Deadline, BudgetMS: item.BudgetMS, Epoch: s.Episode, WindowID: s.WindowID, PolicyRevision: s.PolicyRevision, Probe: s.Controller.Cooling}
	if kind == "ordinary" {
		r.Kind = kind
		r.RequestID = item.RequestID
		r.WorkflowID = "hllm-cpa-request-v2/" + id
	}
	// The V2 window is anchored by a confirmed claim, not a grant or a worker's
	// wall clock. Reuse the existing pacer while accounting its window separately.
	policy := s.Controller.Policy
	s.Controller.Policy.WindowRequests = 0
	s.Controller.Start(now)
	s.Controller.Policy = policy
	if policy.WindowRequests > 0 {
		s.Controller.WindowStarts++
	}
	if r.Probe {
		s.ProbeID = id
		s.ProbeClaimAt = time.Time{}
	}
	s.Outstanding = append(s.Outstanding, PoolPermit{ID: id})
	s.StartIntents = append(s.StartIntents, r)
	return r, nil
}
func (s *PoolState) dropIntent(id string) {
	for i, r := range s.StartIntents {
		if r.PermitID == id {
			s.StartIntents = append(s.StartIntents[:i], s.StartIntents[i+1:]...)
			return
		}
	}
}
func (s *PoolState) Observe(now time.Time, a Attempt) bool {
	index := -1
	for i, p := range s.Outstanding {
		if p.ID == a.PermitID {
			index = i
			break
		}
	}
	if index < 0 || a.PoolID != s.PoolID || a.Generation != s.Generation {
		return false
	}
	changed := false
	if a.ClaimedAt != nil {
		if s.PendingClaimLimit > 0 && !s.Outstanding[index].Claimed {
			s.Outstanding[index].Claimed = true
			changed = true
		}
		s.dropIntent(a.PermitID)
		s.DispatchBackoff = 2 * time.Second
		if a.PermitID == s.ProbeID && s.ProbeClaimAt.IsZero() {
			s.ProbeClaimAt = now
		}
		if a.WindowID == s.WindowID && !s.WindowAnchored {
			s.WindowAnchored = true
			s.Controller.WindowStart = now
			if !s.Controller.Cooling {
				s.Controller.Epoch = now
			}
			changed = true
		}
	}
	if a.State != "settled" || a.Observation == nil || a.SettlementRevision < 1 {
		return changed
	}
	slot := &s.Outstanding[index]
	if a.SettlementRevision <= slot.Revision {
		return changed
	}
	s.dropIntent(a.PermitID)
	if !slot.Applied {
		s.applyOutcome(now, a)
		slot.Applied = true
	}
	slot.Revision = a.SettlementRevision
	slot.AwaitingStop = a.StoppedAt == nil
	if a.StoppedAt != nil {
		s.Outstanding = append(s.Outstanding[:index], s.Outstanding[index+1:]...)
		if a.Kind == "ordinary" {
			s.RequestRetirements = append(s.RequestRetirements, a.PermitID)
		}
	}
	return true
}
func (s *PoolState) applyOutcome(now time.Time, a Attempt) {
	c := &s.Controller
	out := a.Observation.Outcome
	probe := a.Probe && a.PermitID == s.ProbeID
	currentWindow := a.WindowID == s.WindowID
	if out.Kind == "never_dispatched" {
		if currentWindow && c.WindowStarts > 0 {
			c.WindowStarts--
		}
		if probe {
			c.Probing = false
			s.ProbeID = ""
		}
		if a.Kind == "ordinary" {
			return
		}
		if s.CoalesceDispatchFailures && s.DispatchNeedsReady {
			return
		}
		s.DispatchNeedsReady = true
		if s.DispatchBackoff < 2*time.Second {
			s.DispatchBackoff = 2 * time.Second
		}
		s.DispatchBlockedUntil = now.Add(s.DispatchBackoff)
		s.DispatchBackoff = min(30*time.Second, s.DispatchBackoff*2)
		return
	}
	// Capacity settles for every epoch. Only the current recovery probe may
	// reopen cooling; old successes cannot adapt the new episode's launch rate.
	if out.Kind == "completed" && !probe && (c.Cooling || a.Epoch != s.Episode) {
		return
	}
	restricted := out.Kind == "policy" || out.Admission == "restricted"
	throttle := out.Kind == "rejected" || out.Admission == "throttled" || out.RetryAfterMS > 0
	if throttle && !restricted && a.Epoch < s.Episode && !c.Cooling {
		if out.Kind == "rejected" && currentWindow && c.WindowStarts > 0 {
			c.WindowStarts--
		}
		return
	}
	if probe && out.Kind == "completed" && now.Before(c.NextEligible) {
		c.Probing = false
		s.ProbeID = ""
		return
	}
	wasCooling := c.Cooling
	reserved := c.WindowStart.Add(-time.Nanosecond)
	if currentWindow {
		reserved = c.WindowStart
	}
	// Worker clocks do not select an admission episode. The pool has already
	// checked logical ownership and supplies its own deterministic clock here.
	dispatched := c.Epoch
	if probe && !s.ProbeClaimAt.IsZero() {
		dispatched = s.ProbeClaimAt
	}
	c.CompleteOutcome(now, dispatched, reserved, probe, out)
	if !wasCooling && c.Cooling {
		s.Episode++
	}
	if probe && !c.Probing {
		s.ProbeID = ""
	}
}
func (s *PoolState) AcknowledgeStarts(results []AttemptStartResult) {
	for _, r := range results {
		if r.Started {
			s.dropIntent(r.PermitID)
		}
	}
}
func (s *PoolState) Snapshot() ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil || s.PendingClaimLimit < 0 || s.PendingClaimLimit > maximumStartIntents || len(raw) >= maximumPoolSnapshot || len(s.StartIntents) > maximumStartIntents || len(s.Outstanding)+len(s.RequestRetirements) > 1000 {
		return nil, ErrUnavailable
	}
	return raw, nil
}

func (s *PoolState) ReadyRequestExclusions() []string {
	var out []string
	for _, r := range s.StartIntents {
		if r.Kind == "ordinary" {
			out = append(out, r.RequestID)
		}
	}
	return out
}
func (s *PoolState) ReadyExclusions() []Ref {
	out := make([]Ref, 0, len(s.StartIntents))
	for _, r := range s.StartIntents {
		if r.Kind == "job" {
			out = append(out, r.Ref)
		}
	}
	return out
}
