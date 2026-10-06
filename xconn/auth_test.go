package xconn_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/messages"
	xconnd "github.com/xconnio/xconn-go/xconn"
)

const (
	user  = "user"
	admin = "admin"
)

func makeCryptoSignRequest(authid, publicKeyHex string) auth.Request {
	hello := messages.NewHello("realm1", authid, nil, nil, nil)
	return auth.NewCryptoSignRequest(hello, publicKeyHex)
}

func makeBaseRequest(method auth.Method, authid string) auth.Request {
	hello := messages.NewHello("realm1", authid, nil, nil, nil)
	return auth.NewRequest(hello, method)
}

func TestNewAuthenticator(t *testing.T) {
	a := xconnd.NewAuthenticator([]*xconnd.CryptosignPrincipal{
		{AuthID: "user1", AuthorizedKeys: []string{"key1"}, AuthRole: admin},
	}, "")
	require.NotNil(t, a)

	p, ok := a.RetrievePrincipal("user1")
	require.True(t, ok)
	require.Equal(t, admin, p.AuthRole)

	_, ok = a.RetrievePrincipal("nobody")
	require.False(t, ok)
}

func TestAuthenticatorMethods(t *testing.T) {
	methods := xconnd.NewAuthenticator(nil, "").Methods()
	require.Len(t, methods, 1)
	require.Equal(t, auth.Method(auth.MethodCryptoSign), methods[0])
}

func TestAuthenticatorAuthenticate(t *testing.T) {
	pubKey, _, err := auth.GenerateCryptoSignKeyPair()
	require.NoError(t, err)
	otherKey, _, err := auth.GenerateCryptoSignKeyPair()
	require.NoError(t, err)

	a := xconnd.NewAuthenticator([]*xconnd.CryptosignPrincipal{
		{AuthID: "alice", AuthorizedKeys: []string{pubKey}, AuthRole: admin},
	}, "")

	resp, err := a.Authenticate(makeCryptoSignRequest("alice", pubKey))
	require.NoError(t, err)
	require.Equal(t, "alice", resp.AuthID())
	require.Equal(t, admin, resp.AuthRole())

	_, err = a.Authenticate(makeCryptoSignRequest("nobody", pubKey))
	require.ErrorContains(t, err, "unknown authid")

	_, err = a.Authenticate(makeCryptoSignRequest("alice", otherKey))
	require.ErrorContains(t, err, "unknown publickey")

	_, err = a.Authenticate(makeBaseRequest(auth.MethodCryptoSign, "alice"))
	require.ErrorContains(t, err, "invalid request")

	_, err = a.Authenticate(makeBaseRequest(auth.Ticket, "alice"))
	require.ErrorContains(t, err, "unknown authentication method")
}

func TestAuthenticatorSetPrincipals(t *testing.T) {
	a := xconnd.NewAuthenticator([]*xconnd.CryptosignPrincipal{
		{AuthID: "old", AuthorizedKeys: []string{"k"}, AuthRole: user},
	}, "")

	a.SetPrincipals([]*xconnd.CryptosignPrincipal{
		{AuthID: "new1", AuthorizedKeys: []string{"k1"}, AuthRole: admin},
		{AuthID: "new2", AuthorizedKeys: []string{"k2"}, AuthRole: user},
	})

	_, ok := a.RetrievePrincipal("old")
	require.False(t, ok)

	p, ok := a.RetrievePrincipal("new1")
	require.True(t, ok)
	require.Equal(t, admin, p.AuthRole)
}

func TestAuthenticatorSetPrincipal(t *testing.T) {
	a := xconnd.NewAuthenticator(nil, "")
	const key = "key-d"
	a.SetPrincipal("dave", &xconnd.CryptosignPrincipal{
		AuthID: "dave", AuthorizedKeys: []string{key}, AuthRole: user,
	})

	p, ok := a.RetrievePrincipal("dave")
	require.True(t, ok)
	require.Equal(t, []string{key}, p.AuthorizedKeys)

	a.SetPrincipal("dave", &xconnd.CryptosignPrincipal{
		AuthID: "dave", AuthorizedKeys: []string{key, "new-key"}, AuthRole: admin,
	})

	p, ok = a.RetrievePrincipal("dave")
	require.True(t, ok)
	require.Equal(t, admin, p.AuthRole)
	require.Len(t, p.AuthorizedKeys, 2)
}

func principalsFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "principals.json")
}

func TestWriteAndReadPrincipalsRoundTrip(t *testing.T) {
	path := principalsFile(t)

	principals := []*xconnd.CryptosignPrincipal{
		{AuthID: "alice", AuthorizedKeys: []string{"key-a1", "key-a2"}, AuthRole: admin},
		{AuthID: "bob", AuthorizedKeys: []string{"key-b1"}, AuthRole: user},
	}

	require.NoError(t, xconnd.WritePrincipalsToFile(path, principals))

	got, err := xconnd.ReadPrincipalsFromFile(path)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := make(map[string]*xconnd.CryptosignPrincipal, len(got))
	for _, p := range got {
		byID[p.AuthID] = p
	}
	require.Equal(t, []string{"key-a1", "key-a2"}, byID["alice"].AuthorizedKeys)
	require.Equal(t, admin, byID["alice"].AuthRole)
	require.Equal(t, []string{"key-b1"}, byID["bob"].AuthorizedKeys)
}

func TestWritePrincipalsProducesValidJSON(t *testing.T) {
	path := principalsFile(t)

	principals := []*xconnd.CryptosignPrincipal{
		{AuthID: "charlie", AuthorizedKeys: []string{"key-c"}, AuthRole: user},
	}
	require.NoError(t, xconnd.WritePrincipalsToFile(path, principals))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var raw []map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Len(t, raw, 1)
	require.Equal(t, "charlie", raw[0]["authid"])
}

func TestWritePrincipalsEmptyList(t *testing.T) {
	path := principalsFile(t)

	require.NoError(t, xconnd.WritePrincipalsToFile(path, []*xconnd.CryptosignPrincipal{}))

	got, err := xconnd.ReadPrincipalsFromFile(path)
	require.NoError(t, err)
	require.Empty(t, got)
}
