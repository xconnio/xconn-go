package xconn

import "github.com/gobwas/ws/wsutil"

func ClientSideWSReaderWriter(binary bool) (ReaderFunc, WriterFunc, error) {
	if !binary {
		return wsutil.ReadServerText, wsutil.WriteClientText, nil
	}

	return wsutil.ReadServerBinary, wsutil.WriteClientBinary, nil
}

func ServerSideWSReaderWriter(binary bool) (ReaderFunc, WriterFunc, error) {
	if !binary {
		return wsutil.ReadClientText, wsutil.WriteServerText, nil
	}

	return wsutil.ReadClientBinary, wsutil.WriteServerBinary, nil
}
