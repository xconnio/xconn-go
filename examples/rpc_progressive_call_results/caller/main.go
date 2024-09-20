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

	result, err := caller.CallProgress(ctx, procedureProgressDownload, nil, nil, nil, func(result *xconn.Result) {
		progress := result.Arguments[0]
		fmt.Printf("Download progress: %v%%\n", progress)
	})
	if err != nil {
		log.Fatalf("Call failed: %s", err)
	}

	fmt.Println(result.Arguments[0])
}
