package util

import "github.com/xconnio/xconn-go"

type Realm struct {
	Name  string      `yaml:"name"`
	Roles []RealmRole `yaml:"roles"`
}

type Transport struct {
	Type        string        `yaml:"type"`
	Address     string        `yaml:"address"`
	Listener    xconn.Network `yaml:"listener"`
	Serializers []string      `yaml:"serializers"`
	RateLimit   RateLimit     `yaml:"ratelimit"`
}

type RealmRole struct {
	Name        string       `yaml:"name"`
	Permissions []Permission `yaml:"permissions"`
}

type Permission struct {
	URI            string `yaml:"uri"`
	MatchPolicy    string `yaml:"match"`
	AllowCall      bool   `yaml:"allow_call"`
	AllowPublish   bool   `yaml:"allow_publish"`
	AllowRegister  bool   `yaml:"allow_register"`
	AllowSubscribe bool   `yaml:"allow_subscribe"`
}

type RateLimit struct {
	Rate     uint   `yaml:"rate"`
	Interval int    `yaml:"interval"`
	Strategy string `yaml:"strategy"`
}

type CryptoSign struct {
	AuthID         string   `yaml:"authid"`
	Realm          string   `yaml:"realm"`
	Role           string   `yaml:"role"`
	AuthorizedKeys []string `yaml:"authorized_keys"`
}

type WAMPCRA struct {
	AuthID     string `yaml:"authid"`
	Realm      string `yaml:"realm"`
	Role       string `yaml:"role"`
	Secret     string `yaml:"secret"`
	Salt       string `yaml:"salt"`
	Iterations int    `yaml:"iterations"`
	KeyLen     int    `yaml:"keylen"`
}

type Ticket struct {
	AuthID string `yaml:"authid"`
	Realm  string `yaml:"realm"`
	Role   string `yaml:"role"`
	Ticket string `yaml:"ticket"`
}

type Anonymous struct {
	AuthID string `yaml:"authid"`
	Realm  string `yaml:"realm"`
	Role   string `yaml:"role"`
}

type Authenticators struct {
	CryptoSign []CryptoSign `yaml:"cryptosign"`
	WAMPCRA    []WAMPCRA    `yaml:"wampcra"`
	Ticket     []Ticket     `yaml:"ticket"`
	Anonymous  []Anonymous  `yaml:"anonymous"`
}
