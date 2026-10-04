package smoke

import (
	"encoding/hex"
	"strings"
)

// normalizeTempoTraceID restores the leading zeros removed by Tempo's
// TraceIDToHexString, retaining the canonical nonzero 16-byte OTel trace ID.
func normalizeTempoTraceID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 1 || len(value) > 32 || strings.Trim(value, "0") == "" {
		return ""
	}
	value = strings.Repeat("0", 32-len(value)) + value
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}
