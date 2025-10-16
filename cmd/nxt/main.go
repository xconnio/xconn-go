package main

import (
	_ "embed" // nolint:gci
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/alecthomas/kingpin/v2"

	"github.com/xconnio/xconn-go/internal/util"
)

var (
	//go:embed config.yaml.in
	sampleConfig []byte
)

const (
	versionString = "0.1.0"

	DirectoryConfig = ".nxt"
)

type cmd struct {
	parsedCommand string

	init *kingpin.CmdClause

	start     *kingpin.CmdClause
	configDir *string
}

func parseCommand(args []string) (*cmd, error) {
	cwd, _ := os.Getwd()

	app := kingpin.New(args[0], "NXT")
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
		closers, err := util.StartServerFromConfigFile(configFile)
		if err != nil {
			return err
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
