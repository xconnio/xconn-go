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
		chunkIndex := invocation.Arguments[0].(float64)

		if isProgress {
			// Mirror back the received chunk as progress
			fmt.Printf("Received chunk %v, sending progress back\n", chunkIndex)
			if err = invocation.SendProgress([]any{chunkIndex}, nil); err != nil {
				return &xconn.Result{Err: "wamp.error.canceled", Arguments: []any{err.Error()}}
			}

			return &xconn.Result{Err: xconn.ErrNoResult}
		}

		// Final response when all chunks are received
		fmt.Println("All chunks received, processing complete.")
		return &xconn.Result{Arguments: []any{fmt.Sprintf("Upload complete, chunk %v acknowledged", chunkIndex)}}
	}

	request := xconn.NewRegisterRequest(procedureProgressUpload, invocationHandler)
	registration, err := callee.Register(request)
	if err != nil {
		log.Fatalf("Failed to register method: %s", err)
	}
	defer func() { _ = registration.Unregister() }()

	// Wait for interrupt signal to gracefully shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	select {
	case <-sigChan:
		log.Println("Interrupt signal received, shutting down.")
	case <-ctx.Done():
		log.Println("Context canceled, shutting down.")
	}
}
