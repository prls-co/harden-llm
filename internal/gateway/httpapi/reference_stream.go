package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

type referenceOutcome struct {
	output stateful.RunOutput
	state  stateful.RunState
	err    error
}
type referenceEvent struct {
	SchemaVersion int    `json:"schemaVersion"`
	Sequence      uint64 `json:"sequence"`
	RunID         string `json:"runId"`
	CallID        string `json:"callId,omitempty"`
	TraceID       string `json:"traceId,omitempty"`
	Type          string `json:"type"`
	Data          any    `json:"data"`
}

func (api *API) referenceRunSSE(w http.ResponseWriter, r *http.Request, input stateful.RunInput, execution context.Context) {
	ctx, cancel := context.WithCancel(execution)
	defer cancel()
	ready := make(chan struct{}, 1)
	progress := make(chan hardenllm.ProgressEvent, 64)
	terminal := make(chan referenceOutcome, 1)
	outcomes := make(chan referenceOutcome, 1)
	input.Ready = ready
	input.Progress = progress
	input.Completed = func(output stateful.RunOutput, state stateful.RunState, err error) {
		terminal <- referenceOutcome{output: output, state: state, err: err}
	}
	go func() {
		output, state, err := api.service.Run(ctx, scopeFromContext(ctx).hash, input)
		outcomes <- referenceOutcome{output: output, state: state, err: err}
	}()
	var pending *referenceOutcome
	select {
	case <-ready:
	case outcome := <-outcomes:
		if outcome.err != nil {
			api.logRecording(ctx, outcome.output)
			api.referenceRunFailure(w, r, outcome.err, outcome.output)
			return
		}
		pending = &outcome
	case <-r.Context().Done():
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	controller := http.NewResponseController(w)
	if controller.Flush() != nil {
		return
	}
	sequence := uint64(0)
	write := func(kind, runID, callID, traceID string, data any) bool {
		sequence++
		encoded, err := json.Marshal(referenceEvent{SchemaVersion: 1, Sequence: sequence, RunID: runID, CallID: callID, TraceID: traceID, Type: kind, Data: data})
		if err != nil || len(encoded) > maximumResponseBytes {
			return false
		}
		if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, encoded); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	terminalWritten := false
	writeTerminal := func(outcome referenceOutcome) bool {
		if terminalWritten {
			return true
		}
		kind := "run.completed"
		if outcome.err != nil {
			kind = "run.failed"
		}
		data := struct {
			Result stateful.RecordedRunOutput `json:"result"`
			State  stateful.RunState          `json:"state"`
			Error  *stateful.RunError         `json:"error,omitempty"`
		}{Result: outcome.output.RecordedRunOutput, State: outcome.state, Error: stateful.ProjectRunError(outcome.err)}
		terminalWritten = true
		return write(kind, outcome.output.RunID, outcome.output.CallID, outcome.output.TraceID, data)
	}
	drainProgress := func() bool {
		for {
			select {
			case event := <-progress:
				if !terminalWritten && !write("run.progress", event.RunID, event.CallID, event.TraceID, event) {
					return false
				}
			default:
				return true
			}
		}
	}
	complete := func(outcome referenceOutcome) {
		if !drainProgress() || !writeTerminal(outcome) {
			return
		}
		api.logRecording(ctx, outcome.output)
		write("history.recorded", outcome.output.RunID, outcome.output.CallID, outcome.output.TraceID, outcome.output.History)
	}
	if pending != nil {
		complete(*pending)
		return
	}
	for {
		select {
		case event := <-progress:
			if !terminalWritten && !write("run.progress", event.RunID, event.CallID, event.TraceID, event) {
				return
			}
		case outcome := <-terminal:
			if !drainProgress() || !writeTerminal(outcome) {
				return
			}
		case outcome := <-outcomes:
			complete(outcome)
			return
		case <-r.Context().Done():
			return
		}
	}
}
