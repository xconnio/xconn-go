package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/xconn-go"
)

const procedureProgressUpload = "io.xconn.progress.upload"

func main() {
	ctx := context.Background()
	callee, err := xconn.ConnectAnonymous(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = callee.Leave() }()

	invocationHandler := func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
		chunkIndex, _ := invocation.ArgFloat64(0)
		if invocation.Progress() {
			// Mirror back the received chunk as progress
			fmt.Printf("Received chunk %v, sending progress back\n", chunkIndex)
			if err = invocation.SendProgress([]any{chunkIndex}, nil); err != nil {
				return xconn.NewInvocationError("wamp.error.canceled", err.Error())
			}

			return xconn.NewInvocationError(xconn.ErrNoResult)
		}

		// Final response when all chunks are received
		fmt.Println("All chunks received, processing complete.")
		return xconn.NewInvocationResult(fmt.Sprintf("Upload complete, chunk %v acknowledged", chunkIndex))
	}

	registerResponse := callee.Register(procedureProgressUpload, invocationHandler).Do()
	if err != nil {
		log.Fatalf("Failed to register method: %s", err)
	}
	defer func() { _ = registerResponse.Unregister() }()

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
