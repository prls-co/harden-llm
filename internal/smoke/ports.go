package smoke

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

func parseSmokeHTTPSPort(output string) (int, error) {
	host, value, err := net.SplitHostPort(strings.TrimSpace(output))
	port, parseErr := strconv.Atoi(value)
	if err != nil || parseErr != nil || host != "127.0.0.1" || port < 1 || port > 65535 {
		return 0, errors.New("smoke: expected one Docker-assigned loopback HTTPS port")
	}
	return port, nil
}
