package util

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xconnio/xconn-go"
)

func StartServerFromConfigFile(configFile string) ([]io.Closer, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("unable to read config file: %w", err)
	}

	var decoder = yaml.NewDecoder(bytes.NewBuffer(data))
	decoder.KnownFields(true)

	var config Config
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("unable to decode config file: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	router := xconn.NewRouter()
	defer router.Close()

	for _, realm := range config.Realms {
		if err := router.AddRealm(realm.Name); err != nil {
			return nil, fmt.Errorf("unable to add realm: %w", err)
		}
		for _, role := range realm.Roles {
			var permissions []xconn.Permission
			for _, permission := range role.Permissions {
				permissions = append(permissions, xconn.Permission{
					URI:            permission.URI,
					MatchPolicy:    permission.MatchPolicy,
					AllowCall:      permission.AllowCall,
					AllowRegister:  permission.AllowRegister,
					AllowPublish:   permission.AllowPublish,
					AllowSubscribe: permission.AllowSubscribe,
				})
			}
			err = router.AddRealmRole(realm.Name, xconn.RealmRole{
				Name:        role.Name,
				Permissions: permissions,
			})
			if err != nil {
				return nil, err
			}
		}
	}

	authenticator := NewAuthenticator(config.Authenticators)

	closers := make([]io.Closer, 0)
	for _, transport := range config.Transports {
		var throttle *xconn.Throttle
		if transport.RateLimit.Rate > 0 && transport.RateLimit.Interval > 0 {
			strategy := xconn.Burst
			if transport.RateLimit.Strategy == LeakyBucketStrategy {
				strategy = xconn.LeakyBucket
			}
			throttle = xconn.NewThrottle(transport.RateLimit.Rate,
				time.Duration(transport.RateLimit.Interval)*time.Second, strategy)
		}
		server := xconn.NewServer(router, authenticator, &xconn.ServerConfig{Throttle: throttle})

		address := transport.Address
		if transport.Listener == xconn.NetworkUnix {
			address, err = resolveUnixSocketPath(address)
			if err != nil {
				return nil, err
			}
		}
		var closer io.Closer
		switch transport.Type {
		case WebSocketTransport:
			closer, err = server.ListenAndServeWebSocket(transport.Listener, address)
			if err != nil {
				return nil, err
			}
			if transport.Listener == xconn.NetworkUnix {
				fmt.Printf("listening websocket on unix+ws://%s\n", address)
			} else {
				fmt.Printf("listening websocket on ws://%s\n", address)
			}
		case UniversalTransport:
			closer, err = server.ListenAndServeUniversal(transport.Listener, address)
			if err != nil {
				return nil, err
			}
			if transport.Listener == xconn.NetworkUnix {
				fmt.Printf("listening rawsocket on unix://%s\n", address)
				fmt.Printf("listening websocket on unix+ws://%s\n", address)
			} else {
				fmt.Printf("listening rawsocket on rs://%s\n", address)
				fmt.Printf("listening websocket on ws://%s\n", address)
			}
		case RawSocketTransport:
			closer, err = server.ListenAndServeRawSocket(transport.Listener, address)
			if err != nil {
				return nil, err
			}
			if transport.Listener == xconn.NetworkUnix {
				fmt.Printf("listening rawsocket on unix://%s\n", address)
			} else {
				fmt.Printf("listening rawsocket on rs://%s\n", address)
			}
		}

		closers = append(closers, closer)
	}

	return closers, nil
}

func resolveUnixSocketPath(addr string) (string, error) {
	if addr[0] != '~' {
		return addr, nil
	}

	home := os.Getenv("SNAP_REAL_HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}

	return filepath.Join(home, addr[1:]), nil
}
