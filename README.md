# xconn

WAMP v2 Router and Client for go.

## Installation

To install `xconn`, use the following command:

```shell
go get github.com/xconnio/xconn-go
```

## Server

Setting up a basic server is straightforward:

```go
package main

import (
	"log"

	"github.com/xconnio/xconn-go"
)

func main() {
	r := xconn.NewRouter()
	r.AddRealm("realm1")

	server := xconn.NewServer(r, nil)
	err := server.Start("localhost", 8080)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
```

For more advanced usage, such as integrating an authenticator, refer to the sample tool available
in the [cmd](./cmd/xconn) folder of the project.

## Client

Creating a client:

```go
package main

import (
	"context"
	"log"

	"github.com/xconnio/xconn-go"
)

func main() {
	client := xconn.Client{}
	session, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
	if err != nil {
		log.Fatal(err)
	}
}
```

Once the session is established, you can perform WAMP actions. Below are examples of all 4 WAMP
operations:

### Subscribe to a topic

```go
func exampleSubscribe(session *xconn.Session) {
    subscription, err := session.Subscribe("io.xconn.example", eventHandler, map[string]any{})
    if err != nil {
    log.Fatalf("Failed to subscribe: %v", err)
    }
    log.Printf("Subscribed to topic io.xconn.example: %v", subscription)
}

func eventHandler(evt *xconn.Event) {
    fmt.Printf("Event Received: args=%s, kwargs=%s, details=%s", evt.Args, evt.Kwargs, evt.Details)
}
```

### Publish to a topic

```go
func examplePublish(session *xconn.Session) {
    err := session.Publish("io.xconn.example", []any{}, map[string]any{}, map[string]any{})
    if err != nil {
        log.Fatalf("Failed to publish: %v", err)
    }
    log.Printf("Publsihed to topic io.xconn.example")
}
```

### Register a procedure

```go
func exampleRegister(session *xconn.Session) {
    registration, err := session.Register("io.xconn.example", invocationHandler, map[string]any{})
    if err != nil {
        log.Fatalf("Failed to register: %v", err)
    }
    log.Printf("Registered procedure io.xconn.example: %v", registration)
}

func invocationHandler(ctx context.Context, inv *xconn.Invocation) *xconn.Result {
    return &xconn.Result{Args: inv.Args, Kwargs: inv.Kwargs, Details: inv.Details}
}
```

### Call a procedure

```go
func exampleCall(session *xconn.Session) {
    result, err := session.Call(context.Background(), "io.xconn.example", []any{"Hello World!"}, map[string]any{}, map[string]any{})
    if err != nil {
        log.Fatalf("Failed to call: %v", err)
    }
    log.Printf("Call result: args=%s, kwargs=%s, details=%s", result.Args, result.Kwargs, result.Details)
}
```

### Authentication

Authentication is straightforward. Simply create the desired authenticator and pass it
to the Client.

**Ticket Auth**

```go
ticketAuthenticator := auth.NewTicketAuthenticator(authID, map[string]any{}, ticket)
client := xconn.Client{Authenticator: ticketAuthenticator}
session, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
if err != nil {
    log.Fatalf("Failed to connect: %v", err)
}
```

**Challenge Response Auth**

```go
craAuthenticator := auth.NewCRAAuthenticator(authID, map[string]any{}, secret)
client := xconn.Client{Authenticator: craAuthenticator}
session, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
if err != nil {
	log.Fatalf("Failed to connect: %v", err)
}
```

**Cryptosign Auth**

```go
cryptoSignAuthenticator, err := auth.NewCryptoSignAuthenticator(authID, map[string]any{}, secret)
if err != nil {
    log.Fatal(err)
}
client := xconn.Client{Authenticator: cryptoSignAuthenticator}
session, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
if err != nil {
    log.Fatalf("Failed to connect: %v", err)
}
```

### Serializers
XConn supports various serializers for different data formats. To use, just pass chosen serializer spec to the client.

**JSON Serializer**
```go
client := xconn.Client{SerializerSpec: xconn.JSONSerializerSpec}
session, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
if err != nil {
	log.Fatalf("Failed to connect: %v", err)
}
```

**CBOR Serializer**
```go
client := xconn.Client{SerializerSpec: xconn.CBORSerializerSpec}
session, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
if err != nil {
	log.Fatalf("Failed to connect: %v", err)
}
```

**MsgPack Serializer**
```go
client := xconn.Client{SerializerSpec: xconn.MsgPackSerializerSpec}
session, err := client.Connect(context.Background(), "ws://localhost:8080/ws", "realm1")
if err != nil {
	log.Fatalf("Failed to connect: %v", err)
}
```
For more detailed examples or usage, refer to the [examples](./examples) folder of the project.
