package batch

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

func (s *PoolState) padRecoveryFloor(previous time.Time) {
	c := &s.Controller
	if !c.Cooling || c.NextEligible.IsZero() {
		s.RecoveryPadding = nil
		return
	}
	if s.RecoveryPadding != nil && c.NextEligible.Equal(s.RecoveryPadding.Until) {
		return // A selected deadline is immutable across responses/configuration/CAN.
	}
	if !c.NextEligible.After(previous) {
		return
	}
	s.RecoveryPadding = nil
	if s.RecoveryJitterVersion != 1 || c.Policy.RecoveryJitterMS <= 0 || c.Policy.RecoveryJitterMS > 1000 {
		return
	}
	floor := c.NextEligible
	// Version-one bytes/algorithm are part of the replay contract. Future
	// algorithms require another version; never redraw a retained choice.
	seed := fmt.Sprintf("%s\x00%d\x00%s\x00%d\x00%s", s.PoolID, s.Episode, s.ProbeID, s.PolicyRevision, floor.UTC().Format(time.RFC3339Nano))
	digest := sha256.Sum256([]byte(seed))
	padding := time.Duration(binary.BigEndian.Uint64(digest[:8])%uint64(c.Policy.RecoveryJitterMS+1)) * time.Millisecond
	until := floor.Add(padding)
	s.RecoveryPadding = &recoveryPadding{Floor: floor, Until: until}
	c.NextEligible = until
}

func (s PoolState) validRecoveryPadding() bool {
	if s.RecoveryJitterVersion < 0 || s.RecoveryJitterVersion > 1 || s.Controller.Policy.RecoveryJitterMS < 0 || s.Controller.Policy.RecoveryJitterMS > 1000 {
		return false
	}
	p := s.RecoveryPadding
	if p == nil {
		return true
	}
	return s.RecoveryJitterVersion == 1 && !p.Floor.IsZero() && !p.Until.Before(p.Floor) && p.Until.Sub(p.Floor) <= time.Second && s.Controller.Cooling && s.Controller.NextEligible.Equal(p.Until)
}
