package admin

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/layou233/zbproxy/v3/adapter"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/layou233/zbproxy/v3/common/mcprotocol"
	"github.com/layou233/zbproxy/v3/common/network"
	"github.com/layou233/zbproxy/v3/common/network/proxydial"
	"github.com/layou233/zbproxy/v3/config"
)

type ProbeSample struct {
	ConnectMS *float64 `json:"connect_ms,omitempty"`
	PingMS    *float64 `json:"ping_ms,omitempty"`
	Error     string   `json:"error,omitempty"`
}
type ProbeResult struct {
	Name       string        `json:"name"`
	Mode       string        `json:"mode"`
	TestedAt   time.Time     `json:"tested_at"`
	Samples    []ProbeSample `json:"samples"`
	Successful int           `json:"successful"`
	AverageMS  *float64      `json:"average_ms,omitempty"`
}

func (m *NetworkManager) Probe(ctx context.Context, name, mode string) (ProbeResult, error) {
	m.mu.Lock()
	c := cloneRoot(m.current)
	m.mu.Unlock()
	var target *config.Outbound
	for _, out := range c.Outbounds {
		if out.Name == name {
			target = out
			break
		}
	}
	if target == nil {
		return ProbeResult{}, errors.New("出口不存在，请刷新后重试")
	}
	if target.Dialer != "" {
		return ProbeResult{}, errors.New("暂不支持对自定义拨号链测速")
	}
	p := target.ProxyOptions
	dialer, err := proxydial.New(network.NewSystemDialer(target.SocketOptions), p.Type, p.Network, p.Address, p.Username, p.Password)
	if err != nil {
		return ProbeResult{}, err
	}
	result := ProbeResult{Name: name, Mode: mode, TestedAt: time.Now(), Samples: []ProbeSample{}}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var total float64
	for i := 0; i < 3; i++ {
		if ctx.Err() != nil {
			break
		}
		sample := probeOnce(ctx, dialer, target, mode)
		result.Samples = append(result.Samples, sample)
		value := sample.ConnectMS
		if mode == "minecraft" {
			value = sample.PingMS
		}
		if value != nil {
			result.Successful++
			total += *value
		}
	}
	if result.Successful > 0 {
		average := total / float64(result.Successful)
		result.AverageMS = &average
	}
	return result, nil
}

func probeOnce(ctx context.Context, dialer network.Dialer, target *config.Outbound, mode string) (result ProbeSample) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	start := time.Now()
	metadata := &adapter.Metadata{}
	if target.Minecraft != nil && !target.Minecraft.IgnoreSRVRedirect {
		metadata.SRV = &adapter.SRVMetadata{ServiceName: "minecraft"}
	}
	conn, err := adapter.DialContextWithMetadata(dialer, ctx, "tcp", net.JoinHostPort(target.TargetAddress, strconv.Itoa(int(target.TargetPort))), metadata)
	if err != nil {
		result.Error = err.Error()
		return
	}
	defer conn.Close()
	ms := float64(time.Since(start).Microseconds()) / 1000
	result.ConnectMS = &ms
	if mode == "tcp" {
		return
	}
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	defer close(done)
	if err = minecraftStatus(conn, target); err != nil {
		result.Error = err.Error()
		return
	}
	start = time.Now()
	if err = echoPing(conn); err != nil {
		result.Error = err.Error()
	} else {
		ping := float64(time.Since(start).Microseconds()) / 1000
		result.PingMS = &ping
	}
	return
}

func writePacket(w io.Writer, payload []byte) error {
	var packet bytes.Buffer
	mcprotocol.VarInt(len(payload)).WriteTo(&packet)
	packet.Write(payload)
	_, err := w.Write(packet.Bytes())
	return err
}

func readPacket(r io.Reader) ([]byte, error) {
	size, _, err := mcprotocol.ReadVarIntFrom(r)
	if err != nil {
		return nil, err
	}
	if size < 1 || size > 1<<20 {
		return nil, errors.New("MC 响应长度无效")
	}
	data := make([]byte, int(size))
	_, err = io.ReadFull(r, data)
	return data, err
}

func minecraftStatus(conn net.Conn, target *config.Outbound) error {
	var handshake bytes.Buffer
	handshake.WriteByte(0)
	mcprotocol.VarInt(47).WriteTo(&handshake)
	host := target.TargetAddress
	if target.Minecraft != nil && target.Minecraft.EnableHostnameRewrite && target.Minecraft.RewrittenHostname != "" {
		host = target.Minecraft.RewrittenHostname
	}
	mcprotocol.VarInt(len(host)).WriteTo(&handshake)
	handshake.WriteString(host)
	binary.Write(&handshake, binary.BigEndian, target.TargetPort)
	handshake.WriteByte(1)
	if err := writePacket(conn, handshake.Bytes()); err != nil {
		return err
	}
	if err := writePacket(conn, []byte{0}); err != nil {
		return err
	}
	data, err := readPacket(conn)
	if err != nil {
		return fmt.Errorf("读取 MC 状态：%w", err)
	}
	reader := bytes.NewReader(data)
	id, _, err := mcprotocol.ReadVarIntFrom(reader)
	if err != nil || id != 0 {
		return errors.New("MC 状态响应无效")
	}
	size, _, err := mcprotocol.ReadVarIntFrom(reader)
	if err != nil || int(size) != reader.Len() {
		return errors.New("MC 状态内容长度无效")
	}
	status := make([]byte, reader.Len())
	io.ReadFull(reader, status)
	if !json.Valid(status) {
		return errors.New("MC 状态不是有效 JSON")
	}
	return nil
}

func echoPing(conn net.Conn) error {
	ping := make([]byte, 9)
	ping[0] = 1
	binary.BigEndian.PutUint64(ping[1:], uint64(time.Now().UnixNano()))
	if err := writePacket(conn, ping); err != nil {
		return err
	}
	data, err := readPacket(conn)
	if err != nil {
		return fmt.Errorf("读取 MC Ping：%w", err)
	}
	if !bytes.Equal(data, ping) {
		return errors.New("MC Ping 响应不匹配")
	}
	return nil
}
