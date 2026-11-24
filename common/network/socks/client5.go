package socks

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

const (
	version5                         byte = 5
	AuthTypeNotRequired              byte = 0
	AuthTypeGSSAPI                   byte = 1
	AuthTypeUsernamePassword         byte = 2
	AuthTypeNoAcceptedMethods        byte = 255
	UsernamePasswordStatusSuccess    byte = 0
	UsernamePasswordStatusFailure    byte = 1
	CommandConnect                   byte = 1
	CommandBind                      byte = 2
	CommandUDPAssociate              byte = 3
	AddressTypeIPv4                  byte = 1
	AddressTypeDomain                byte = 3
	AddressTypeIPv6                  byte = 4
	ReplyCode5Success                byte = 0
	ReplyCode5Failure                byte = 1
	ReplyCode5NotAllowed             byte = 2
	ReplyCode5NetworkUnreachable     byte = 3
	ReplyCode5HostUnreachable        byte = 4
	ReplyCode5ConnectionRefused      byte = 5
	ReplyCode5TTLExpired             byte = 6
	ReplyCode5Unsupported            byte = 7
	ReplyCode5AddressTypeUnsupported byte = 8
)

func (c *Client) handshake5(r io.Reader, w io.Writer, network, address string) error {
	host, portString, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		return err
	}
	if len(c.Username) > 255 || len(c.Password) > 255 {
		return errors.New("socks: credentials exceed 255 bytes")
	}
	methods := c.Methods
	if len(methods) == 0 {
		methods = []byte{AuthTypeNotRequired}
		if c.Username != "" || c.Password != "" {
			methods = []byte{AuthTypeUsernamePassword}
		}
	}
	if _, err = w.Write(append([]byte{version5, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	var reply [2]byte
	if _, err = io.ReadFull(r, reply[:]); err != nil {
		return err
	}
	if reply[0] != version5 {
		return errors.New("socks: invalid server version")
	}
	accepted := false
	for _, method := range methods {
		if method == reply[1] {
			accepted = true
		}
	}
	if !accepted {
		return errors.New("socks: proxy rejected authentication method")
	}
	switch reply[1] {
	case AuthTypeNotRequired:
	case AuthTypeUsernamePassword:
		if c.Username == "" || c.Password == "" {
			return errors.New("socks: username and password are required")
		}
		auth := append([]byte{1, byte(len(c.Username))}, []byte(c.Username)...)
		auth = append(auth, byte(len(c.Password)))
		auth = append(auth, []byte(c.Password)...)
		if _, err = w.Write(auth); err != nil {
			return err
		}
		if _, err = io.ReadFull(r, reply[:]); err != nil {
			return err
		}
		if reply[0] != 1 || reply[1] != 0 {
			return errors.New("socks: username/password authentication failed")
		}
	default:
		return errors.New("socks: unsupported authentication method")
	}
	request := []byte{version5, CommandConnect, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(append(request, AddressTypeIPv4), ip4...)
		} else {
			request = append(append(request, AddressTypeIPv6), ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return errors.New("socks: invalid target hostname length")
		}
		request = append(request, AddressTypeDomain, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err = w.Write(request); err != nil {
		return err
	}
	var header [4]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		return err
	}
	if header[0] != version5 || header[2] != 0 {
		return errors.New("socks: invalid CONNECT response")
	}
	if header[1] != 0 {
		return fmt.Errorf("socks: CONNECT failed (code %d)", header[1])
	}
	var size int64
	switch header[3] {
	case AddressTypeIPv4:
		size = 4
	case AddressTypeIPv6:
		size = 16
	case AddressTypeDomain:
		var length [1]byte
		if _, err = io.ReadFull(r, length[:]); err != nil {
			return err
		}
		size = int64(length[0])
	default:
		return errors.New("socks: invalid bound address type")
	}
	_, err = io.CopyN(io.Discard, r, size+2)
	return err
}
