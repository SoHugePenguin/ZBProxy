package config

import (
	"net"
	"strconv"
)

type Web struct {
	Enabled   bool
	Bind      string
	Port      uint16
	AccessLog bool
	Username  string
	Password  string
}

func DefaultWeb() Web {
	return Web{Enabled: true, Bind: "127.0.0.1", Port: 8080, AccessLog: true, Username: "admin"}
}

func (w Web) Address() string {
	return net.JoinHostPort(w.Bind, strconv.Itoa(int(w.Port)))
}
