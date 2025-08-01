package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/xconn-go"
)

const procedureProgressUpload = "io.xconn.progress.upload"

func main() {
	ctx := context.Background()
	caller, err := xconn.ConnectAnonymous(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = caller.Leave() }()

	totalChunks := 5
	chunkIndex := 0

	fmt.Println("Starting file upload...")

	callResponse := caller.Call(procedureProgressUpload).
		ProgressSender(func(ctx context.Context) *xconn.Progress {
			options := map[string]any{}

			// Mark the last chunk as non-progressive
			if chunkIndex == totalChunks-1 {
				options[wampproto.OptionProgress] = false
			} else {
				options[wampproto.OptionProgress] = true
			}

			// Simulate uploading chunk
			fmt.Printf("Sending chunk %d\n", chunkIndex)
			args := []any{chunkIndex}
			chunkIndex++

			// Simulate delay for each chunk
			time.Sleep(500 * time.Millisecond)

			return &xconn.Progress{Args: args, Options: options}
		}).ProgressReceiver(func(result *xconn.InvocationResult) {
		// Handle progress updates mirrored by the callee
		chunkProgress := result.Args[0].(float64)
		fmt.Printf("Progress update: chunk %v acknowledged by server\n", chunkProgress)
	}).Do()

	if callResponse.Err != nil {
		log.Fatalf("Failed to upload data: %s", callResponse.Err)
	}

	fmt.Printf("Upload complete: %s\n", callResponse.Args[0])
}
