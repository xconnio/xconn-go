package main

import (
	"bytes"
	_ "embed" // nolint:gci
	"fmt"
	"log"
	"os"

	"github.com/alecthomas/kingpin/v2"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"

	"github.com/xconnio/wampproto-protobuf/go"
	"github.com/xconnio/xconn-go"
)

var (
	//go:embed config.yaml.in
	sampleConfig []byte
)

const (
	versionString = "0.1.0"

	ConfigDir  = ".xconn"
	ConfigFile = ConfigDir + "/config.yaml"

	ProtobufSubProtocol = "wamp.2.protobuf"
)

type cmd struct {
	parsedCommand string

	init *kingpin.CmdClause

	start *kingpin.CmdClause
}

func parseCommand(args []string) (*cmd, error) {
	app := kingpin.New(args[0], "XConn")
	app.Version(versionString).VersionFlag.Short('v')

	c := &cmd{
		init:  app.Command("init", "Initialize sample router config."),
		start: app.Command("start", "Start the router."),
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

	switch c.parsedCommand {
	case c.init.FullCommand():
		if err := os.MkdirAll(ConfigDir, os.ModePerm); err != nil {
			return err
		}

		if err = os.WriteFile(ConfigFile, sampleConfig, 0600); err != nil {
			return fmt.Errorf("unable to write config: %w", err)
		}

	case c.start.FullCommand():
		data, err := os.ReadFile(ConfigFile)
		if err != nil {
			return fmt.Errorf("unable to read config file: %w", err)
		}

		var decoder = yaml.NewDecoder(bytes.NewBuffer(data))
		decoder.KnownFields(true)

		var config Config
		if err := decoder.Decode(&config); err != nil {
			return fmt.Errorf("unable to decode config file: %w", err)
		}

		router := xconn.NewRouter()

		for _, realm := range config.Realms {
			router.AddRealm(realm.Name)
		}

		authenticator := NewAuthenticator(config.Authenticators)
		server := xconn.NewServer(router, authenticator)

		for _, transport := range config.Transports {
			if slices.Contains(transport.Serializers, "protobuf") {
				serializer := &wampprotobuf.ProtobufSerializer{}
				protobufSpec := xconn.NewWSSerializerSpec(ProtobufSubProtocol, serializer)

				if err := server.RegisterSpec(protobufSpec); err != nil {
					return err
				}
			}

			if err := server.Start("0.0.0.0", transport.Port); err != nil {
				return err
			}
		}

	}

	return nil
}

func main() {
	if err := Run(os.Args); err != nil {
		log.Fatalln(err)
	}
}
