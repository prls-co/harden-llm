package runtime

import (
	"context"
	"time"

	"github.com/prls-co/harden-llm/internal/retry"
)

type progressEmitter struct {
	ctx          context.Context
	now          func() time.Time
	startedAt    time.Time
	record       *CallRecord
	report       func(ProgressSnapshot)
	runID        string
	maxAttempts  int
	sequence     uint64
	activeStream *StreamDiagnostics
}

func newProgressEmitter(ctx context.Context, call Call, config retry.Config, record *CallRecord, startedAt time.Time) progressEmitter {
	return progressEmitter{
		ctx: ctx, now: config.Now, startedAt: startedAt, record: record,
		report: call.Progress, runID: call.Context.RunID, maxAttempts: config.Policy.MaxAttempts,
	}
}

func (progress *progressEmitter) streamCallback(emit func()) func(StreamDiagnostics) {
	return func(snapshot StreamDiagnostics) {
		progress.activeStream = &snapshot
		emit()
	}
}

func (progress *progressEmitter) emit(event ProgressSnapshot) {
	if progress.report == nil {
		return
	}
	progress.sequence++
	record, now := progress.record, progress.now
	event.Sequence = progress.sequence
	event.RunID = progress.runID
	event.CallID = record.CallID
	event.TraceID = record.TraceID
	event.MaxAttempts = progress.maxAttempts

	var accountingSnapshot *Accounting
	if len(record.Attempts) > 0 {
		value := record.Accounting
		accountingSnapshot = &value
	}
	var effectiveTimeout *time.Duration
	if record.Diagnostics.EffectiveTimeout != nil {
		value := *record.Diagnostics.EffectiveTimeout
		effectiveTimeout = &value
	}

	receivedBytes := record.Diagnostics.ReceivedBytes
	eventCount := record.Diagnostics.EventCount
	outputBytes := record.Diagnostics.OutputBytes
	outputCodePoints := record.Diagnostics.OutputCodePoints
	if activeStream := progress.activeStream; activeStream != nil {
		receivedBytes += activeStream.ReceivedBytes
		eventCount += activeStream.EventCount
		outputBytes += activeStream.OutputBytes
		outputCodePoints += activeStream.OutputCodePoints
	}

	event.AttemptsUsed = len(record.Attempts)
	event.AttemptsRemaining = max(event.MaxAttempts-len(record.Attempts), 0)
	event.ElapsedMs = max(now().Sub(progress.startedAt).Milliseconds(), 0)
	event.StopReason = record.StopReason
	event.ReceivedBytes = receivedBytes
	event.EventCount = eventCount
	event.OutputBytes = outputBytes
	event.OutputCodePoints = outputCodePoints
	event.LastActivity = now()
	event.EffectiveTimeout = effectiveTimeout
	event.Origin = record.Origin
	event.Attempts = append([]AttemptRecord(nil), record.Attempts...)
	event.Accounting = accountingSnapshot

	if deadline, ok := progress.ctx.Deadline(); ok {
		remaining := max(deadline.Sub(now()).Milliseconds(), 0)
		event.DeadlineRemainingMs = &remaining
	}
	progress.report(event)
}
