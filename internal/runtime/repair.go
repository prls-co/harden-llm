package runtime

import (
	"encoding/json"
	"strings"
)

func buildRepairRequest(attempt, maxAttempts int, previousOutput string, failure error, call Call) *RepairRequest {
	request := &RepairRequest{
		Attempt: attempt, MaxAttempts: maxAttempts, PreviousOutput: previousOutput,
		TargetSchema: append(json.RawMessage(nil), call.Schema...),
	}
	if failure != nil {
		// The current validator reports one error. Bound its feedback without
		// nesting earlier repair requests or adding a second error collector.
		feedback := failure.Error()
		request.ValidationFeedback = strings.ToValidUTF8(feedback[:min(len(feedback), 8<<10)], "")
	}
	return request
}
