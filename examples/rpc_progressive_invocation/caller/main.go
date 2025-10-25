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

	totalChunks := 6
	chunkIndex := 0

	// Simulate file data being uploaded in chunks
	log.Println("Starting file upload...")

	callResponse := caller.Call(procedureProgressUpload).
		ProgressSender(func(ctx context.Context) *xconn.Progress {
			// Simulate sending each chunk of the file
			log.Printf("Uploading chunk %d...", chunkIndex)
			defer func() { chunkIndex++ }()

			// Simulate network delay between chunks
			time.Sleep(500 * time.Millisecond)

			if chunkIndex == totalChunks-1 {
				return xconn.NewFinalProgress(chunkIndex)
			} else {
				return xconn.NewProgress(chunkIndex)
			}
		}).Do()
	if callResponse.Err != nil {
		log.Fatalf("Failed to upload data: %s", callResponse.Err)
	}

	log.Println("Final result:", callResponse.ArgStringOr(0, ""))
}
