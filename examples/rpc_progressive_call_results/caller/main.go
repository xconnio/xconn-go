package main

import (
	"context"
	"fmt"
	"log"

	"github.com/xconnio/xconn-go"
)

const procedureProgressDownload = "io.xconn.progress.download"

func main() {
	// Create and connect a caller client to server
	ctx := context.Background()
	client := xconn.Client{}
	caller, err := client.Connect(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = caller.Leave() }()

	callRequest := xconn.NewCallRequest(procedureProgressDownload).ProgressReceiver(func(result *xconn.Result) {
		progress := result.Arguments[0]
		fmt.Printf("Download progress: %v%%\n", progress)
	})
	result, err := caller.Call(ctx, callRequest)
	if err != nil {
		log.Fatalf("CallRaw failed: %s", err)
	}

	fmt.Println(result.Arguments[0])
}
