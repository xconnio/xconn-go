# xconn

WAMP v2 Router and Client for go.

## Installation

To install `xconn`, use the following command:

```shell
go get github.com/xconnio/xconn-go
```

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
	session, err := xconn.ConnectAnonymous(context.Background(), "ws://localhost:8080/ws", "realm1")
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
    subscribeResponse := session.Subscribe("io.xconn.example", eventHandler).Do()
    if subscribeResponse.Err != nil {
        log.Fatalf("Failed to subscribe: %v", subscribeResponse.Err)
    }
    log.Printf("Subscribed to topic io.xconn.example")
}

func eventHandler(evt *xconn.Event) {
    fmt.Printf("Event Received: args=%s, kwargs=%s, details=%s", evt.Args, evt.Kwargs, evt.Details)
}
```

### Publish to a topic

```go
func examplePublish(session *xconn.Session) {
    publishResponse := session.Publish("io.xconn.example").Arg("test").Do()
    if publishResponse.Err != nil {
        log.Fatalf("Failed to publish: %v", publishResponse.Err)
    }
    log.Printf("Published to topic io.xconn.example")
}
```

### Register a procedure

```go
func exampleRegister(session *xconn.Session) {
    registerResponse := session.Register("io.xconn.example", invocationHandler).Do()
    if registerResponse.Err != nil {
        log.Fatalf("Failed to register: %v", registerResponse.Err)
    }
    log.Printf("Registered procedure io.xconn.example")
}

func invocationHandler(ctx context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
    return xconn.NewInvocationResult()
}
```

### Call a procedure

```go
func exampleCall(session *xconn.Session) {
    callResponse := session.Call("io.xconn.example").Arg("Hello World!").Do()
    if callResponse.Err != nil {
        log.Fatalf("Failed to call: %v", callResponse.Err)
    }
    log.Printf("Call result: args=%s, kwargs=%s, details=%s", callResponse.Args, callResponse.Kwargs, callResponse.Details)
}
```

### Authentication

Authentication is straightforward.

**Ticket Auth**

```go
session, err := xconn.ConnectTicket(context.Background(), "ws://localhost:8080/ws", "realm1", "authID", "ticket")
if err != nil {
    log.Fatalf("Failed to connect: %v", err)
}
```

**Challenge Response Auth**

```go
session, err := xconn.ConnectCRA(context.Background(), "ws://localhost:8080/ws", "realm1", "authID", "secret")
if err != nil {
	log.Fatalf("Failed to connect: %v", err)
}
```

**Cryptosign Auth**

```go
session, err := xconn.ConnectCryptosign(context.Background(), "ws://localhost:8080/ws", "realm1", "authID", "privateKey")
if err != nil {
    log.Fatalf("Failed to connect: %v", err)
}
```

## Standalone Router
This repo contains a command-line tool to run the router with a static config.
```bash
git clone git@github.com:xconnio/xconn-go
cd xconn-go
make build
./nxt init
./nxt start
```
After running `nxt init` the static yaml config will appear at `.nxt/config.yaml`

For more detailed examples or usage, refer to the [examples](./examples) folder of the project.
