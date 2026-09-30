package auth

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/prls-co/prls-control-plane/go/access"
)

const productID = "harden-llm"

var (
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	ErrForbidden       = errors.New("auth: forbidden")
	ErrUnavailable     = errors.New("auth: identity unavailable")
	accountIDPattern   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type Config struct {
	ControlPlane    *access.Client
	ServiceToken    string
	StaticAccountID string
}

type Service struct {
	controlPlane    *access.Client
	serviceToken    string
	staticAccountID string
}

// Principal contains only the resource scope established for this request.
type Principal struct {
	OwnerID string
}

func ValidateServiceToken(token, accountID string) error {
	if accountID != "" && (token == "" || ValidateAccountID(accountID) != nil) {
		return errors.New("auth: static account ID requires a valid service token")
	}
	if token != "" && !validServiceToken(token) {
		return errors.New("auth: service token configuration is invalid")
	}
	return nil
}

// ValidateAccountID accepts the UUID identity used by Control Plane accounts.
func ValidateAccountID(accountID string) error {
	if !accountIDPattern.MatchString(accountID) {
		return errors.New("auth: account ID must be a UUID")
	}
	return nil
}

func NewService(config Config) (*Service, error) {
	if config.ControlPlane == nil {
		return nil, errors.New("auth: Control Plane client is required")
	}
	if err := ValidateServiceToken(config.ServiceToken, config.StaticAccountID); err != nil {
		return nil, err
	}
	return &Service{
		controlPlane: config.ControlPlane, serviceToken: config.ServiceToken,
		staticAccountID: config.StaticAccountID,
	}, nil
}

// AuthenticateRequest resolves every human request against the current Control
// Plane session. A token without a session reference is the separate machine
// credential path and is scoped to its configured account.
func (service *Service) AuthenticateRequest(request *http.Request) (Principal, error) {
	if service == nil || request == nil || len(request.Header.Values("Authorization")) != 1 ||
		len(request.Header.Values("Cookie")) != 0 {
		return Principal{}, ErrUnauthenticated
	}
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") || strings.Count(value, " ") != 1 {
		return Principal{}, ErrUnauthenticated
	}
	token := strings.TrimPrefix(value, "Bearer ")
	if strings.TrimSpace(token) != token || token == "" {
		return Principal{}, ErrUnauthenticated
	}
	if !access.ServiceAuthorized(request, service.serviceToken) {
		return Principal{}, ErrUnauthenticated
	}

	sessionReferences := request.Header.Values("X-PRLS-Session-Reference")
	if len(sessionReferences) != 0 {
		if len(sessionReferences) != 1 || strings.TrimSpace(sessionReferences[0]) != sessionReferences[0] || sessionReferences[0] == "" {
			return Principal{}, ErrUnauthenticated
		}
		context, err := service.controlPlane.Authorize(request, service.serviceToken, productID)
		if err != nil {
			switch {
			case errors.Is(err, access.ErrUnauthenticated):
				return Principal{}, ErrUnauthenticated
			case errors.Is(err, access.ErrForbidden):
				return Principal{}, ErrForbidden
			default:
				return Principal{}, ErrUnavailable
			}
		}
		if context.Account == nil {
			return Principal{}, ErrForbidden
		}
		return Principal{OwnerID: context.Account.ID}, nil
	}

	if service.staticAccountID == "" {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{OwnerID: service.staticAccountID}, nil
}

func validServiceToken(token string) bool {
	if len(token) < 32 || len(token) > 512 || strings.TrimSpace(token) != token {
		return false
	}
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
