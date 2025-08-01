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

	totalChunks := 6
	chunkIndex := 0

	// Simulate file data being uploaded in chunks
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

			// Simulate sending each chunk of the file
			fmt.Printf("Uploading chunk %d...\n", chunkIndex)
			args := []any{chunkIndex}
			chunkIndex++

			// Simulate network delay between chunks
			time.Sleep(500 * time.Millisecond)

			return &xconn.Progress{Arguments: args, Options: options}
		}).Do()
	if callResponse.Err != nil {
		log.Fatalf("Failed to upload data: %s", callResponse.Err)
	}

	fmt.Println("Final result:", callResponse.Arguments[0])
}
