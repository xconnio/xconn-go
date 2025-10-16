package main

import (
	_ "embed" // nolint:gci
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/xconn-go/internal/util"
)

var (
	//go:embed config.yaml.in
	sampleConfig []byte
)

const (
	versionString   = "0.1.0"
	DirectoryConfig = ".nxt"
)

func printUsage(cwd string) {
	usage := fmt.Sprintf(`usage: nxt [<flags>] <command> [<args> ...]

NXT

Flags:
      --help             Show context-sensitive help.
  -v, --version          Show application version.
  -c, --config="%s"
                         Set config directory.

Commands:
help [<command>...]
    Show help.

init
    Initialize sample router config.

start
    Start the router.


`, cwd)
	fmt.Print(usage)
}

func handleGlobalFlags(args []string) error {
	for _, arg := range args {
		switch arg {
		case "-v", "--version":
			fmt.Println(versionString)
			return nil
		case "-h", "--help", "help":
			cwd, _ := os.Getwd()
			printUsage(cwd)
			return nil
		}
	}
	return nil
}

func Run(args []string) error {
	cwd, _ := os.Getwd()

	if len(args) < 2 {
		printUsage(cwd)
		return nil
	}

	if err := handleGlobalFlags(args[1:]); err != nil {
		return err
	}

	cmd := args[1]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	configDir := fs.String("config", cwd, "Set config directory")
	fs.StringVar(configDir, "c", cwd, "Set config directory (shorthand)")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	configDirPath := filepath.Join(*configDir, DirectoryConfig)
	configFile := filepath.Join(configDirPath, "config.yaml")

	switch cmd {
	case "init":
		if err := os.MkdirAll(configDirPath, os.ModePerm); err != nil {
			return err
		}
		if err := os.WriteFile(configFile, sampleConfig, 0600); err != nil {
			return fmt.Errorf("unable to write config: %w", err)
		}
	case "start":
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
	default:
		printUsage(cwd)
	}

	return nil
}

func main() {
	if err := Run(os.Args); err != nil {
		log.Fatalln(err)
	}
}
