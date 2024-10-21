package xconn

const (
	JsonWebsocketProtocol    = "wamp.2.json"
	MsgpackWebsocketProtocol = "wamp.2.msgpack"
	CborWebsocketProtocol    = "wamp.2.cbor"
	ProtobufSubProtocol      = "wamp.2.protobuf"

	CloseGoodByeAndOut = "wamp.close.goodbye_and_out"
	CloseCloseRealm    = "wamp.close.close_realm"
)
