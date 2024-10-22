package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/xconn-go"
)

const procedureProgressUpload = "io.xconn.progress.upload"

func main() {
	ctx := context.Background()
	client := xconn.Client{}
	callee, err := client.Connect(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = callee.Leave() }()

	invocationHandler := func(ctx context.Context, invocation *xconn.Invocation) *xconn.Result {
		isProgress, _ := invocation.Details[wampproto.OptionProgress].(bool)

		// Handle the progressive chunk
		if isProgress {
			chunkIndex := invocation.Arguments[0].(float64)
			fmt.Printf("Received chunk %v\n", chunkIndex)
			return &xconn.Result{Err: xconn.ErrNoResult}
		}

		// Final response after all chunks are received
		fmt.Println("All chunks received, processing complete.")
		return &xconn.Result{Arguments: []any{"Upload complete"}}
	}

	registration, err := callee.Register(procedureProgressUpload, invocationHandler, nil)
	if err != nil {
		log.Fatalf("Failed to register procedure: %s", err)
	}
	defer func() { _ = callee.Unregister(registration.ID) }()

	// Wait for interrupt signal to gracefully shut down
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	select {
	case <-sigChan:
		log.Println("Interrupt signal received, shutting down.")
	case <-ctx.Done():
		log.Println("Context canceled, shutting down.")
	}
}
