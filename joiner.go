package xconn

import (
	"context"
	netURL "net/url"
	"time"

	"github.com/gobwas/ws"
)

func DialWebSocket(ctx context.Context, url *netURL.URL, config WSDialerConfig) (Peer, error) {
	wsDialer := ws.Dialer{
		Protocols: []string{config.SubProtocol},
	}

	if config.DialTimeout == 0 {
		wsDialer.Timeout = time.Second * 10
	} else {
		wsDialer.Timeout = config.DialTimeout
	}

	conn, _, _, err := wsDialer.Dial(ctx, url.String())
	if err != nil {
		return nil, err
	}

	isBinary := config.SubProtocol != JsonWebsocketProtocol
	return NewWebSocketPeer(conn, config.SubProtocol, isBinary, false)
}
