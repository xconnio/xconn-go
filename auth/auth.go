package auth

import "github.com/xconnio/wampproto-go/auth"

var (
	NewAnonymousAuthenticator  = auth.NewAnonymousAuthenticator  //nolint:gochecknoglobals
	NewTicketAuthenticator     = auth.NewTicketAuthenticator     //nolint:gochecknoglobals
	NewCRAAuthenticator        = auth.NewCRAAuthenticator        //nolint:gochecknoglobals
	NewCryptoSignAuthenticator = auth.NewCryptoSignAuthenticator //nolint:gochecknoglobals
)
