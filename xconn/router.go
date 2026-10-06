package xconn

import (
	"fmt"
	"net"
	"net/url"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go"
)

// ServeRouter serves the device realm on rawURL to clients holding one of keys: xconn's
// router on its own, with no cloud connection or app layer. The
// scheme picks the transport: ws:// and rs:// listen on TCP, unix+ws:// and unix:// (or
// unix+rs://) on a Unix socket. stop shuts the router down.
func ServeRouter(rawURL, realm string, keys []string) (addr net.Addr, stop func(), err error) {
	if len(keys) == 0 {
		return nil, nil, fmt.Errorf("at least one key is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid url %q: %w", rawURL, err)
	}

	router, err := NewDeviceRouter(realm)
	if err != nil {
		return nil, nil, err
	}
	authenticator := NewStandaloneAuthenticator(keys, nil)
	server := xconn.NewServer(router, authenticator, &xconn.ServerConfig{})

	var listener *xconn.Listener
	switch u.Scheme {
	case "ws":
		listener, err = server.ListenAndServeWebSocket(xconn.NetworkTCP, u.Host)
	case "rs":
		listener, err = server.ListenAndServeRawSocket(xconn.NetworkTCP, u.Host)
	case "unix+ws":
		listener, err = server.ListenAndServeWebSocket(xconn.NetworkUnix, u.Path)
	case networkUnix, "unix+rs":
		listener, err = server.ListenAndServeRawSocket(xconn.NetworkUnix, u.Path)
	default:
		err = fmt.Errorf("unsupported url scheme %q (use ws, rs, unix, unix+ws or unix+rs)", u.Scheme)
	}
	if err != nil {
		router.Close()
		return nil, nil, err
	}
	stop = func() {
		_ = listener.Close()
		router.Close()
	}

	session, err := xconn.ConnectInMemory(router, realm)
	if err != nil {
		stop()
		return nil, nil, err
	}
	if err := SetupWebRTC(session, router, authenticator, nil, ""); err != nil {
		stop()
		return nil, nil, err
	}
	return listener.Addr(), stop, nil
}

// standaloneAuthRole is the role every client of a standaloneAuthenticator gets.
const standaloneAuthRole = "owner"

// standaloneAuthenticator accepts cryptosign clients holding one of a fixed set of public
// keys, whatever authid they present, and wampcra clients with a fixed set of
// username/password pairs.
type standaloneAuthenticator struct {
	keys      map[string]bool
	passwords map[string]string // username -> password
}

// NewStandaloneAuthenticator returns a standaloneAuthenticator for keys and passwords
// (username -> password).
func NewStandaloneAuthenticator(keys []string, passwords map[string]string) auth.ServerAuthenticator {
	a := &standaloneAuthenticator{keys: make(map[string]bool, len(keys)), passwords: passwords}
	for _, k := range keys {
		a.keys[k] = true
	}
	return a
}

func (a *standaloneAuthenticator) Methods() []auth.Method {
	var methods []auth.Method
	if len(a.keys) > 0 {
		methods = append(methods, auth.MethodCryptoSign)
	}
	if len(a.passwords) > 0 {
		methods = append(methods, auth.WAMPCRA)
	}
	return methods
}

func (a *standaloneAuthenticator) Authenticate(request auth.Request) (auth.Response, error) {
	switch r := request.(type) {
	case *auth.RequestCryptoSign:
		if !a.keys[r.PublicKey()] {
			return nil, fmt.Errorf("unknown publickey")
		}
		return auth.NewResponse(r.AuthID(), standaloneAuthRole, 0)
	default:
		if request.AuthMethod() != auth.WAMPCRA {
			return nil, fmt.Errorf("unsupported authmethod %s", request.AuthMethod())
		}
		password, ok := a.passwords[request.AuthID()]
		if !ok {
			return nil, fmt.Errorf("unknown authid %s", request.AuthID())
		}
		// The acceptor verifies the client's signature over the challenge with password.
		return auth.NewCRAResponse(request.AuthID(), standaloneAuthRole, password, 0), nil
	}
}
