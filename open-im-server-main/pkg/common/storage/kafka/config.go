package kafka

type TLSConfig struct {
	EnableTLS          bool   `yaml:"enableTLS"`
	CACrt              string `yaml:"caCrt"`
	ClientCrt          string `yaml:"clientCrt"`
	ClientKey          string `yaml:"clientKey"`
	ClientKeyPwd       string `yaml:"clientKeyPwd"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

type Config struct {
	Username     string    `yaml:"username"`
	Password     string    `yaml:"password"`
	ProducerAck  string    `yaml:"producerAck"`
	CompressType string    `yaml:"compressType"`
	Addr         []string  `yaml:"addr"`
	TLS          TLSConfig `yaml:"tls"`
}
