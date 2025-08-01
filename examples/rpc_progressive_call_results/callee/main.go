package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/xconnio/xconn-go"
)

const procedureProgressDownload = "io.xconn.progress.download"

func main() {
	// Create and connect a callee client to server
	ctx := context.Background()
	client := xconn.Client{}
	callee, err := client.Connect(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = callee.Leave() }()

	invocationHandler := func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
		fileSize := 100 // Simulate a file size of 100 units
		for i := 0; i <= fileSize; i += 10 {
			progress := i * 100 / fileSize
			if err := invocation.SendProgress([]any{progress}, nil); err != nil {
				return xconn.NewInvocationError("wamp.error.canceled", err.Error())
			}
			time.Sleep(500 * time.Millisecond) // Simulate time taken for download
		}

		return xconn.NewInvocationResult("Download complete!")
	}

	registerResponse := callee.Register(procedureProgressDownload, invocationHandler).Do()
	if err != nil {
		log.Fatalf("Failed to register method: %s", err)
	}
	defer func() { _ = registerResponse.Unregister() }()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	select {
	case <-sigChan:
	case <-ctx.Done():
	}
}
