package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go"
)

const (
	procedureHandshake = "io.xconn.progress.handshake"

	publicKeyHex  = "7752b16addeeb89e42ef50a84b5bbf1ea53404bc5c416a9fd30efa7dd6bcbcca"
	privateKeyHex = "873e63d8e42daf58c3c9d3d43cafc82062e30f6059d447f7debce910a52c0c24"
)

func main() {
	privateKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		log.Fatalf("Failed to decode private key: %v", err)
	}
	privateKey := ed25519.NewKeyFromSeed(privateKeyBytes)

	caller, err := xconn.ConnectAnonymous(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatalf("Failed to connect to server: %s", err)
	}
	defer func() { _ = caller.Leave() }()

	challengeChan := make(chan string, 1)
	step := 0

	callResponse := caller.Call(procedureHandshake).
		ProgressSender(func(ctx context.Context) *xconn.Progress {
			switch step {
			case 0:
				step++
				return &xconn.Progress{
					Args:    []any{publicKeyHex},
					Options: map[string]any{wampproto.OptionProgress: true},
				}
			case 1:
				challenge := <-challengeChan

				signature, _ := auth.SignCryptoSignChallenge(challenge, privateKey)

				step++
				return &xconn.Progress{
					Args:    []any{signature},
					Options: map[string]any{wampproto.OptionProgress: false},
				}
			default:
				return nil
			}
		}).
		ProgressReceiver(func(result *xconn.InvocationResult) {
			ch := util.ToString(result.Args[0])
			challengeChan <- ch
		}).
		Do()

	if callResponse.Err != nil {
		log.Fatalf("Authentication failed: %v", callResponse.Err)
	}

	log.Println("Signature verified.")
}
