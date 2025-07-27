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
	client := xconn.Client{}
	caller, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	// callWithRequest procedure "io.xconn.echo"
	echoResult, err := caller.Call(testProcedureEcho).Do()
	if err != nil {
		log.Fatalf("Failed to call %s: %s", testProcedureEcho, err)
	}
	log.Printf("Result of procedure %s: args=%s, kwargs=%s, details=%s", testProcedureEcho, echoResult.Arguments,
		echoResult.KwArguments, echoResult.Details)

	// callWithRequest procedure "io.xconn.sum"
	sumResult, err := caller.Call(testProcedureSum).Do()
	if err != nil {
		log.Fatalf("Failed to call %s: %s", testProcedureSum, err)
	}
	log.Printf("Sum=%s", sumResult.Arguments[0])

	// Close connection to the server
	err = caller.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
