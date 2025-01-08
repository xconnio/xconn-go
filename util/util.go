package util

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"

	"github.com/xconnio/xconn-go"
	"github.com/xconnio/xconn-go/internal"
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
		router.AddRealm(realm.Name)
	}

	authenticator := NewAuthenticator(config.Authenticators)

	closers := make([]io.Closer, 0)
	for _, transport := range config.Transports {
		var throttle *internal.Throttle
		if transport.RateLimit.Rate > 0 && transport.RateLimit.Interval > 0 {
			strategy := internal.Burst
			if transport.RateLimit.Strategy == LeakyBucketStrategy {
				strategy = internal.LeakyBucket
			}
			throttle = internal.NewThrottle(transport.RateLimit.Rate,
				time.Duration(transport.RateLimit.Interval)*time.Second, strategy)
		}
		server := xconn.NewServer(router, authenticator, &xconn.ServerConfig{Throttle: throttle})
		if slices.Contains(transport.Serializers, "protobuf") {
			if err := server.RegisterSpec(xconn.ProtobufSerializerSpec); err != nil {
				return nil, err
			}
		}

		closer, err := server.Start(transport.Host, transport.Port)
		if err != nil {
			return nil, err
		}

		closers = append(closers, closer)
	}

	return closers, nil
}
