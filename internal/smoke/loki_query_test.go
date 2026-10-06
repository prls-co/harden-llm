package smoke

import "testing"

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-410
func TestLokiCorrelatedResponseLog(t *testing.T) {
	traceID := "trace-expected"
	body := []byte(`{"status":"success","data":{"result":[{"stream":{"service_name":"harden-llm-gateway","route":"/v1/responses","trace_id":"trace-expected"},"values":[["1","http request completed"]]}]}}`)
	if !hasCorrelatedResponseLog(body, traceID) {
		t.Fatal("matching trace, route, and request event in one Loki entry was not accepted")
	}
	separateStreams := []byte(`{"status":"success","data":{"result":[{"stream":{"route":"/v1/responses"},"values":[["1","http request completed"]]},{"stream":{"trace_id":"trace-expected"},"values":[["2","http request completed"]]}]}}`)
	if hasCorrelatedResponseLog(separateStreams, traceID) {
		t.Fatal("trace and route from separate Loki streams were combined")
	}
	for _, invalid := range []string{"", "{}", `{"status":"error"}`, `{"status":"success","data":{"result":[]}}`} {
		if hasCorrelatedResponseLog([]byte(invalid), traceID) {
			t.Fatalf("invalid Loki result %q was accepted", invalid)
		}
	}
}
