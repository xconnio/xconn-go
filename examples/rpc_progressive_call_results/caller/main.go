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
	caller, err := xconn.ConnectAnonymous(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = caller.Leave() }()

	callResponse := caller.Call(procedureProgressDownload).ProgressReceiver(func(result *xconn.InvocationResult) {
		progress := result.Args[0]
		fmt.Printf("Download progress: %v%%\n", progress)
	}).Do()
	if callResponse.IsError() {
		log.Fatalf("CallRaw failed: %s", callResponse.Error().Error())
	}

	fmt.Println(callResponse.Args[0])
}
