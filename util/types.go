package util

type Realm struct {
	Name string `yaml:"name"`
}

type Transport struct {
	Type        string    `yaml:"type"`
	Host        string    `yaml:"host"`
	Port        int       `yaml:"port"`
	Serializers []string  `yaml:"serializers"`
	RateLimit   RateLimit `yaml:"ratelimit"`
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
