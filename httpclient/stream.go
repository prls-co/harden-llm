package httpclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type runEvent struct {
	SchemaVersion int             `json:"schemaVersion"`
	Sequence      uint64          `json:"sequence"`
	RunID         string          `json:"runId"`
	CallID        string          `json:"callId,omitempty"`
	TraceID       string          `json:"traceId,omitempty"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
}

// streamReader keeps only the current frame. Limits apply before an unbounded
// line/frame allocation, independently of socket chunking or UTF-8 boundaries.
type streamReader struct {
	reader *bufio.Reader
	bytes  int
}

func (stream *streamReader) frame() (string, []byte, error) {
	var kind string
	var data bytes.Buffer
	frameSize := 0
	for {
		var line []byte
		for {
			part, err := stream.reader.ReadSlice('\n')
			stream.bytes += len(part)
			frameSize += len(part)
			if stream.bytes > maximumStreamBytes || frameSize > maximumResponseBytes {
				return "", nil, &ProtocolError{}
			}
			line = append(line, part...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				return "", nil, err
			}
			break
		}
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if text == "" {
			if data.Len() == 0 {
				kind = ""
				frameSize = 0
				continue
			}
			return kind, bytes.TrimSuffix(data.Bytes(), []byte("\n")), nil
		}
		if strings.HasPrefix(text, ":") {
			continue
		}
		field, value, found := strings.Cut(text, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			kind = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
}

func readRunStream(reader io.Reader, input RunInput) (RunOutcome, error) {
	stream := streamReader{reader: bufio.NewReader(reader)}
	outcome := RunOutcome{}
	var sequence uint64
	var runID, callID, traceID string
	var terminalFailure *RunFailure
	terminal := false
	finish := func(cause error) (RunOutcome, error) {
		if !terminal {
			return RunOutcome{}, &UnknownOutcomeError{Cause: cause}
		}
		if terminalFailure != nil {
			return outcome, terminalFailure
		}
		return outcome, nil
	}
	for {
		kind, data, err := stream.frame()
		if err != nil {
			return finish(err)
		}
		var event runEvent
		if decodeWire("ReferenceRunProgressEvent", data, &event) != nil || event.Type != kind || event.Sequence <= sequence ||
			(runID != "" && event.RunID != runID) || (callID != "" && event.CallID != "" && event.CallID != callID) || (traceID != "" && event.TraceID != "" && event.TraceID != traceID) {
			return finish(&ProtocolError{})
		}
		sequence = event.Sequence
		runID = event.RunID
		if event.CallID != "" {
			callID = event.CallID
		}
		if event.TraceID != "" {
			traceID = event.TraceID
		}
		if terminal {
			if kind != "history.recorded" {
				return finish(&ProtocolError{})
			}
			var recording RecordingOutcome
			if decodeWire("RecordingOutcome", event.Data, &recording) != nil || recording.RunID != outcome.Result.RunID || event.CallID != outcome.Result.CallID || event.TraceID != outcome.Result.TraceID {
				return finish(&ProtocolError{})
			}
			outcome.Recording = recording
			return finish(nil)
		}
		switch kind {
		case "run.started", "run.progress":
			// The outer envelope is the protocol; data is diagnostic only.
			continue
		case "run.completed", "run.failed":
			var payload struct {
				Result json.RawMessage `json:"result"`
				State  json.RawMessage `json:"state"`
				Error  json.RawMessage `json:"error,omitempty"`
			}
			decoder := json.NewDecoder(bytes.NewReader(event.Data))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&payload) != nil || decodeWire("RecordedRunOutput", payload.Result, &outcome.Result) != nil || decodeWire("ReferenceRunState", payload.State, &outcome.State) != nil {
				return finish(&ProtocolError{})
			}
			outcome.Result.Raw = bytes.Clone(payload.Result)
			if event.RunID != outcome.Result.RunID || event.CallID != outcome.Result.CallID || event.TraceID != outcome.Result.TraceID ||
				outcome.State.LastRunID != outcome.Result.RunID || outcome.State.LastTraceID != outcome.Result.TraceID {
				return finish(&ProtocolError{})
			}
			if kind == "run.completed" {
				if outcome.Result.Status != "succeeded" || len(payload.Error) != 0 || validateStructured(input, outcome.Result.Output) != nil {
					return finish(&ProtocolError{})
				}
			} else {
				var problem RunError
				if outcome.Result.Status == "succeeded" || decodeWire("ReferenceError", payload.Error, &problem) != nil {
					return finish(&ProtocolError{})
				}
				terminalFailure = &RunFailure{Problem: problem}
			}
			evidence, err := projectEvidence(outcome.Result, input)
			if err != nil {
				return finish(err)
			}
			outcome.Evidence = evidence
			if terminalFailure != nil {
				terminalFailure.Evidence = evidence
			}
			outcome.Recording = RecordingOutcome{Mode: "enabled", Status: "unconfirmed", RunID: outcome.Result.RunID}
			terminal = true
		default:
			return finish(&ProtocolError{})
		}
	}
}
