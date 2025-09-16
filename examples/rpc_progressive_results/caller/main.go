package main

import (
	"context"
	"fmt"
	"log"
	"time"

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
			// Simulate uploading chunk
			fmt.Printf("Sending chunk %d\n", chunkIndex)
			defer func() { chunkIndex++ }()

			// Simulate delay for each chunk
			time.Sleep(500 * time.Millisecond)

			if chunkIndex == totalChunks-1 {
				return xconn.NewFinalProgress(chunkIndex)
			} else {
				return xconn.NewProgress(chunkIndex)
			}
		}).ProgressReceiver(func(result *xconn.InvocationResult) {
		// Handle progress updates mirrored by the callee
		chunkProgress := result.Args[0].(uint64)
		fmt.Printf("Progress update: chunk %v acknowledged by server\n", chunkProgress)
	}).Do()

	if callResponse.IsError() {
		log.Fatalf("Failed to upload data: %s", callResponse.Error().Error())
	}

	fmt.Printf("Upload complete: %s\n", callResponse.Args[0])
}
