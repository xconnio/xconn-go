package util

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/exp/slices"
)

const (
	JsonSerializer     = "json"
	CborSerializer     = "cbor"
	MsgPackSerializer  = "msgpack"
	ProtobufSerializer = "protobuf"

	BurstStrategy       = "burst"
	LeakyBucketStrategy = "leakybucket"
)

var URIRegex = regexp.MustCompile(`^([^\s.#]+\.)*([^\s.#]+)$`)

type Config struct {
	Version        string         `yaml:"version"`
	Realms         []Realm        `yaml:"realms"`
	Transports     []Transport    `yaml:"transports"`
	Authenticators Authenticators `yaml:"authenticators"`
}

func (c Config) Validate() error {
	for _, realm := range c.Realms {
		if !URIRegex.MatchString(realm.Name) {
			return fmt.Errorf("invalid realm %s: must be a valid URI", realm.Name)
		}
	}

	for _, transport := range c.Transports {
		if err := validateTransport(transport); err != nil {
			return fmt.Errorf("invalid transport: %w", err)
		}
	}

	if err := validateAuthenticators(c.Authenticators); err != nil {
		return err
	}

	return nil
}

func validateSerializers(serializers []string) error {
	var allowedSerializer = map[string]bool{
		JsonSerializer:     true,
		CborSerializer:     true,
		MsgPackSerializer:  true,
		ProtobufSerializer: true,
	}

	for _, elem := range serializers {
		if !allowedSerializer[elem] {
			return fmt.Errorf("invalid serializer: %s", elem)
		}
	}

	return nil
}

func validateTransport(transport Transport) error {
	if transport.Type != "websocket" {
		return fmt.Errorf("type is required and must be 'websocket'")
	}

	if err := validateSerializers(transport.Serializers); err != nil {
		return err
	}

	allowedStrategies := []string{"", BurstStrategy, LeakyBucketStrategy}
	if !slices.Contains(allowedStrategies, transport.RateLimit.Strategy) {
		return fmt.Errorf("invalid rate limit strategy '%s', must be one of '%s' or '%s'",
			transport.RateLimit.Strategy, BurstStrategy, LeakyBucketStrategy)
	}

	return nil
}

func validateNonEmptyNoSpaceString(field, fieldName string) error {
	if strings.TrimSpace(field) == "" {
		return fmt.Errorf("%s is required", fieldName)
	}
	if strings.Contains(field, " ") {
		return fmt.Errorf("%s must not contain empty spaces", fieldName)
	}
	return nil
}

func validateCommonFields(authid, realm, role string) error {
	if err := validateNonEmptyNoSpaceString(authid, "authid"); err != nil {
		return err
	}

	if !URIRegex.MatchString(realm) {
		return fmt.Errorf("invalid realm %s: must be a valid URI", realm)
	}

	return validateNonEmptyNoSpaceString(role, "role")
}

func validateAuthorizedKeys(authorizedKeys []string) error {
	if len(authorizedKeys) == 0 {
		return fmt.Errorf("no authorized keys provided")
	}
	for _, pubKey := range authorizedKeys {
		publicKeyRaw, err := hex.DecodeString(pubKey)
		if err != nil {
			return fmt.Errorf("invalid public key: %w", err)
		}
		if len(publicKeyRaw) != 32 {
			return fmt.Errorf("invalid public key: public key must have length of 32")
		}
	}

	return nil
}

func validateAuthenticators(authenticators Authenticators) error {
	for _, auth := range authenticators.Anonymous {
		if err := validateCommonFields(auth.AuthID, auth.Realm, auth.Role); err != nil {
			return fmt.Errorf("invalid anonymous authenticator: %w", err)
		}
	}

	for _, auth := range authenticators.Ticket {
		if err := validateCommonFields(auth.AuthID, auth.Realm, auth.Role); err != nil {
			return fmt.Errorf("invalid ticket authenticator: %w", err)
		}
		if strings.TrimSpace(auth.Ticket) == "" {
			return fmt.Errorf("invalid ticket authenticator: ticket is required")
		}
	}

	for _, auth := range authenticators.WAMPCRA {
		if err := validateCommonFields(auth.AuthID, auth.Realm, auth.Role); err != nil {
			return fmt.Errorf("invalid wampcra authenticator: %w", err)
		}
		if strings.TrimSpace(auth.Secret) == "" {
			return fmt.Errorf("invalid wampcra authenticator: secret is required")
		}
	}

	for _, auth := range authenticators.CryptoSign {
		if err := validateCommonFields(auth.AuthID, auth.Realm, auth.Role); err != nil {
			return fmt.Errorf("invalid cryptoSign authenticator: %w", err)
		}
		if err := validateAuthorizedKeys(auth.AuthorizedKeys); err != nil {
			return fmt.Errorf("invalid cryptoSign authenticator: %w", err)
		}
	}

	return nil
}
