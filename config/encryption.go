package config

// Encryption terminates Trojan TCP over verified TLS before Minecraft routing.
// Disabled or omitted preserves the existing plaintext listener.
type Encryption struct {
	Enabled  bool
	CertFile string `json:",omitempty"`
	KeyFile  string `json:",omitempty"`
	Password string `json:",omitempty"`
}

// EncryptionClient is used only with the -client command-line option.
type EncryptionClient struct {
	Enabled       bool
	Bind          string
	Listen        uint16
	ServerAddress string
	ServerPort    uint16
	ServerName    string
	Password      string
	CACertFile    string `json:",omitempty"`
}
