package xconn

import "net"

func NewBaseSession(id int64, realm, authID, authRole string, cl Peer) BaseSession {
	return &baseSession{
		id:       id,
		realm:    realm,
		authID:   authID,
		authRole: authRole,
		client:   cl,
	}
}

type baseSession struct {
	id                      int64
	realm, authID, authRole string

	client Peer
}

func (b *baseSession) ID() int64 {
	return b.id
}

func (b *baseSession) Realm() string {
	return b.realm
}

func (b *baseSession) AuthID() string {
	return b.authID
}

func (b *baseSession) AuthRole() string {
	return b.authRole
}

func (b *baseSession) NetConn() net.Conn {
	return b.client.NetConn()
}

func (b *baseSession) Read() ([]byte, error) {
	return b.client.Read()
}

func (b *baseSession) Write(payload []byte) error {
	return b.client.Write(payload)
}
