package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

func newGatewayID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", errors.New("gateway: random ID generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
