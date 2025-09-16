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

	totalChunks := 6
	chunkIndex := 0

	// Simulate file data being uploaded in chunks
	fmt.Println("Starting file upload...")

	callResponse := caller.Call(procedureProgressUpload).
		ProgressSender(func(ctx context.Context) *xconn.Progress {
			// Simulate sending each chunk of the file
			fmt.Printf("Uploading chunk %d...\n", chunkIndex)
			defer func() { chunkIndex++ }()

			// Simulate network delay between chunks
			time.Sleep(500 * time.Millisecond)

			if chunkIndex == totalChunks-1 {
				return xconn.NewFinalProgress(chunkIndex)
			} else {
				return xconn.NewProgress(chunkIndex)
			}
		}).Do()
	if callResponse.IsError() {
		log.Fatalf("Failed to upload data: %s", callResponse.Error().Error())
	}

	fmt.Println("Final result:", callResponse.Args[0])
}
