package main

import (
	"context"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/xconn-go"
)

const testProcedureEcho = "io.xconn.echo"
const testProcedureSum = "io.xconn.sum"

func main() {
	// Create and connect a caller client to server
	caller, err := xconn.ConnectAnonymous(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}

	// Call procedure "io.xconn.echo"
	echoResponse := caller.Call(testProcedureEcho).Do()
	if echoResponse.Err != nil {
		log.Fatalf("Failed to call %s: %s", testProcedureEcho, echoResponse.Err)
	}
	log.Printf("Result of procedure %s: args=%s, kwargs=%s, details=%s", testProcedureEcho, echoResponse.Args,
		echoResponse.Kwargs, echoResponse.Details)

	// Call procedure "io.xconn.sum"
	sumResponse := caller.Call(testProcedureSum).Arg(1).Arg(2).Do()
	if sumResponse.Err != nil {
		log.Fatalf("Failed to call %s: %s", testProcedureSum, sumResponse.Err)
	}
	log.Printf("Sum=%s", sumResponse.Args[0])

	// Close connection to the server
	err = caller.Leave()
	if err != nil {
		log.Fatalf("Failed to leave server: %s", err)
	}
}
