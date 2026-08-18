package smoke

import (
	"encoding/hex"
	"strings"
)

// normalizeTempoTraceID restores one leading zero nibble omitted by some Tempo
// JSON responses while retaining the canonical 16-byte OTel trace-ID form.
func normalizeTempoTraceID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 31 && len(value) != 32 {
		return ""
	}
	value = strings.Repeat("0", 32-len(value)) + value
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}
