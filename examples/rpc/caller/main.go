package main

import (
	"context"
	"log"

	"github.com/xconnio/xconn-go"
)

const testProcedureEcho = "io.xconn.echo"
const testProcedureSum = "io.xconn.sum"

func main() {
	// Create and connect a caller client to server
	ctx := context.Background()
	client := xconn.Client{}
	caller, err := client.Connect(ctx, "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	// Call procedure "io.xconn.echo"
	echoResponse := caller.CallRaw(ctx, testProcedureEcho, []any{}, map[string]any{}, map[string]any{})
	if echoResponse.Err != nil {
		log.Fatalf("Failed to call %s: %s", testProcedureEcho, echoResponse.Err)
	}
	log.Printf("Result of procedure %s: args=%s, kwargs=%s, details=%s", testProcedureEcho, echoResponse.Arguments,
		echoResponse.KwArguments, echoResponse.Details)

	// Call procedure "io.xconn.sum"
	sumResponse := caller.CallRaw(ctx, testProcedureSum, []any{}, map[string]any{}, map[string]any{})
	if sumResponse.Err != nil {
		log.Fatalf("Failed to call %s: %s", testProcedureSum, sumResponse.Err)
	}
	log.Printf("Sum=%s", sumResponse.Arguments[0])

	// Close connection to the server
	err = caller.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
