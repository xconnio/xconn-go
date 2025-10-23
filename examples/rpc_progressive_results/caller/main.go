package main

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

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

	log.Println("Starting file upload...")

	callResponse := caller.Call(procedureProgressUpload).
		ProgressSender(func(ctx context.Context) *xconn.Progress {
			// Simulate uploading chunk
			log.Printf("Sending chunk %d", chunkIndex)
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
		log.Printf("Progress update: chunk %v acknowledged by server", chunkProgress)
	}).Do()

	if callResponse.Err != nil {
		log.Fatalf("Failed to upload data: %s", callResponse.Err)
	}

	log.Printf("Upload complete: %s", callResponse.Args[0])
}
