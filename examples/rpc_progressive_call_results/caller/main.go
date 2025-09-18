package main

import (
	"context"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/xconn-go"
)

const procedureProgressDownload = "io.xconn.progress.download"

func main() {
	// Create and connect a caller client to server
	ctx := context.Background()
	caller, err := xconn.ConnectAnonymous(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = caller.Leave() }()

	callResponse := caller.Call(procedureProgressDownload).ProgressReceiver(func(result *xconn.ProgressResult) {
		progress := result.ArgUInt64Or(0, 0)
		log.Printf("Download progress: %v%%", progress)
	}).Do()
	if callResponse.Err != nil {
		log.Fatalf("CallRaw failed: %s", callResponse.Err)
	}

	log.Println(callResponse.ArgStringOr(0, ""))
}
