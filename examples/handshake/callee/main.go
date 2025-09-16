package main

import (
	"context"
	"encoding/hex"
	"log"
	"os"
	"os/signal"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go"
)

const procedureHandshake = "io.xconn.progress.handshake"

func main() {
	callee, err := xconn.ConnectAnonymous(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = callee.Leave() }()

	var hexPublicKey string

	handler := func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
		isProgress, _ := invocation.Details()[wampproto.OptionProgress].(bool)

		if isProgress {
			hexPublicKey = util.ToString(invocation.Args()[0])
			challenge, _ := auth.GenerateCryptoSignChallenge()

			if err := invocation.SendProgress([]any{challenge}, nil); err != nil {
				return xconn.NewInvocationError(wampproto.ErrCanceled, err.Error())
			}

			return xconn.NewInvocationError(xconn.ErrNoResult)
		}

		signature := util.ToString(invocation.Args()[0])
		publicKeyBytes, err := hex.DecodeString(hexPublicKey)
		if err != nil {
			return xconn.NewInvocationError(wampproto.ErrInvalidArgument, "invalid public key format")
		}

		verified, err := auth.VerifyCryptoSignSignature(signature, publicKeyBytes)
		if err != nil {
			return xconn.NewInvocationError("wamp.error.verification_failed", err.Error())
		}
		if !verified {
			return xconn.NewInvocationError("wamp.error.verification_failed", "signature verification failed")
		}

		return xconn.NewInvocationResult()
	}

	reg := callee.Register(procedureHandshake, handler).Do()
	if reg.IsError() {
		log.Fatalf("Failed to register procedure: %v", reg.Error())
	}
	defer func() { _ = reg.Unregister() }()

	// Wait for interrupt signal (Ctrl+C)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
