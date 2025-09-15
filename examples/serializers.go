package examples

import (
	"context"

	"github.com/xconnio/xconn-go"
)

func connectJSON(url, realm string) (*xconn.Session, error) {
	client := xconn.Client{SerializerSpec: xconn.JSONSerializerSpec}

	return client.Connect(context.Background(), url, realm)
}

func connectCBOR(url, realm string) (*xconn.Session, error) {
	client := xconn.Client{SerializerSpec: xconn.CBORSerializerSpec}

	return client.Connect(context.Background(), url, realm)
}

func connectMsgPack(url, realm string) (*xconn.Session, error) {
	client := xconn.Client{SerializerSpec: xconn.MsgPackSerializerSpec}

	return client.Connect(context.Background(), url, realm)
}
