package main

import (
	"bytes"
	_ "embed" // nolint:gci
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"

	"github.com/xconnio/xconn-go"
	"github.com/xconnio/xconn-go/internal"
)

var (
	//go:embed config.yaml.in
	sampleConfig []byte
)

const (
	versionString = "0.1.0"

	DirectoryConfig = ".xconn"
)

type cmd struct {
	parsedCommand string

	init *kingpin.CmdClause

	start     *kingpin.CmdClause
	configDir *string
}

func parseCommand(args []string) (*cmd, error) {
	cwd, _ := os.Getwd()

	app := kingpin.New(args[0], "XConn")
	app.Version(versionString).VersionFlag.Short('v')

	c := &cmd{
		init:      app.Command("init", "Initialize sample router config."),
		start:     app.Command("start", "Start the router."),
		configDir: app.Flag("config", "Set config directory").Default(cwd).Short('c').String(),
	}

	parsedCommand, err := app.Parse(args[1:])
	if err != nil {
		return nil, err
	}
	c.parsedCommand = parsedCommand

	return c, nil
}

func Run(args []string) error {
	c, err := parseCommand(args)
	if err != nil {
		return err
	}
	configDir := filepath.Join(*c.configDir, DirectoryConfig)
	configFile := filepath.Join(configDir, "config.yaml")

	switch c.parsedCommand {
	case c.init.FullCommand():
		if err := os.MkdirAll(configDir, os.ModePerm); err != nil {
			return err
		}

		if err = os.WriteFile(configFile, sampleConfig, 0600); err != nil {
			return fmt.Errorf("unable to write config: %w", err)
		}

	case c.start.FullCommand():
		data, err := os.ReadFile(configFile)
		if err != nil {
			return fmt.Errorf("unable to read config file: %w", err)
		}

		var decoder = yaml.NewDecoder(bytes.NewBuffer(data))
		decoder.KnownFields(true)

		var config Config
		if err := decoder.Decode(&config); err != nil {
			return fmt.Errorf("unable to decode config file: %w", err)
		}

		if err := config.Validate(); err != nil {
			return fmt.Errorf("invalid config: %w", err)
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
			server := xconn.NewServer(router, authenticator, throttle)
			if slices.Contains(transport.Serializers, "protobuf") {
				if err := server.RegisterSpec(xconn.ProtobufSerializerSpec); err != nil {
					return err
				}
			}

			closer, err := server.Start(transport.Host, transport.Port)
			if err != nil {
				return err
			}

			closers = append(closers, closer)
		}

		// Close server if SIGINT (CTRL-c) received.
		closeChan := make(chan os.Signal, 1)
		signal.Notify(closeChan, os.Interrupt)
		<-closeChan

		for _, closer := range closers {
			_ = closer.Close()
		}
	}

	return nil
}

func main() {
	if err := Run(os.Args); err != nil {
		log.Fatalln(err)
	}
}
