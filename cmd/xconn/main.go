package main

import (
	_ "embed" // nolint:gci
	"fmt"
	"log"
	"os"

	"github.com/alecthomas/kingpin/v2"
)

var (
	//go:embed config.yaml.in
	sampleConfig []byte
)

const (
	versionString = "0.1.0"

	ConfigDir  = ".xconn"
	ConfigFile = ConfigDir + "/config.yaml"
)

type cmd struct {
	parsedCommand string

	init *kingpin.CmdClause
}

func parseCommand(args []string) (*cmd, error) {
	app := kingpin.New(args[0], "XConn")
	app.Version(versionString).VersionFlag.Short('v')

	c := &cmd{
		init: app.Command("init", "Initialize sample router config."),
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
	}

	return nil
}

func main() {
	if err := Run(os.Args); err != nil {
		log.Fatalln(err)
	}
}
