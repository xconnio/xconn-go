package nxt

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
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

	routerConfig := &xconn.RouterConfig{}
	if config.Config.Management {
		routerConfig.Management = true
	}
	if config.Config.Loglevel != "" {
		loglevel, err := parseLogLevel(config.Config.Loglevel)
		if err != nil {
			return nil, err
		}
		routerConfig.LogLevel = loglevel
	}
	router, err := xconn.NewRouter(routerConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create router: %w", err)
	}

	for _, realm := range config.Realms {
		if err := router.AddRealm(realm.Name, xconn.DefaultRealmConfig()); err != nil {
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
		var listener *xconn.Listener
		switch transport.Type {
		case WebSocketTransport:
			listener, err = server.ListenAndServeWebSocket(transport.Listener, address)
			if err != nil {
				return nil, err
			}
			if transport.Listener == xconn.NetworkUnix {
				log.Printf("listening websocket on unix+ws://%s", address)
			} else {
				log.Printf("listening websocket on ws://%s", address)
			}
		case UniversalTransport:
			listener, err = server.ListenAndServeUniversal(transport.Listener, address)
			if err != nil {
				return nil, err
			}
			if transport.Listener == xconn.NetworkUnix {
				log.Printf("listening rawsocket on unix://%s", address)
				log.Printf("listening websocket on unix+ws://%s", address)
			} else {
				log.Printf("listening rawsocket on rs://%s", address)
				log.Printf("listening websocket on ws://%s", address)
			}
		case RawSocketTransport:
			listener, err = server.ListenAndServeRawSocket(transport.Listener, address)
			if err != nil {
				return nil, err
			}

			if transport.Listener == xconn.NetworkUnix {
				log.Printf("listening rawsocket on unix://%s", address)
			} else {
				log.Printf("listening rawsocket on rs://%s", address)
			}
		}

		closers = append(closers, listener)
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

func parseLogLevel(s string) (xconn.LogLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return xconn.LogLevelTrace, nil
	case "debug":
		return xconn.LogLevelDebug, nil
	case "info":
		return xconn.LogLevelInfo, nil
	case "warn":
		return xconn.LogLevelWarn, nil
	case "error":
		return xconn.LogLevelError, nil
	case "":
		return xconn.LogLevelInfo, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: must be one of: 'trace', 'debug', 'info', 'warn', 'error'", s)
	}
}
