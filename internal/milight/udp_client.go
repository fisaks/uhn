package milight

import (
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/logging"
)

// UDPClient sends commands to a Mi-Light iBox2 bridge via UDP v6 protocol.
type UDPClient struct {
	host string
	port int

	mu        sync.Mutex
	conn      *net.UDPConn
	sessionID [2]byte // ephemeral, from most recent handshake response bytes 19:21
	seqNum    byte    // wraps 1–255
}

func NewUDPClient(host string, port int) *UDPClient {
	return &UDPClient{host: host, port: port}
}

// Connect opens the UDP socket.
func (c *UDPClient) Connect() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", c.host, c.port))
	if err != nil {
		return fmt.Errorf("resolve UDP addr: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return fmt.Errorf("dial UDP: %w", err)
	}
	c.conn = conn
	c.seqNum = 0
	return nil
}

// Handshake performs the v6 session handshake (0x20 → 0x28 response).
// Extracts the 2-byte session ID from the response.
func (c *UDPClient) Handshake(timeout time.Duration) error {
	// v6 handshake packet (must match the 27-byte format used by iBox2 apps)
	packet := []byte{
		0x20, 0x00, 0x00, 0x00, 0x16, 0x02, 0x62, 0x3A,
		0xD5, 0xED, 0xA3, 0x01, 0xAE, 0x08, 0x2D, 0x46,
		0x61, 0x41, 0xA7, 0xF6, 0xDC, 0xAF, 0xD3, 0xE6,
		0x00, 0x00, 0x1E,
	}

	if err := c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if _, err := c.conn.Write(packet); err != nil {
		return fmt.Errorf("send handshake: %w", err)
	}

	buf := make([]byte, 64)
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	n, err := c.conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read handshake response: %w", err)
	}

	logging.Debug("Mi-Light handshake response", "hex", hex.EncodeToString(buf[:n]), "len", n)

	// Response should start with 0x28 and be >= 22 bytes
	if n < 22 || buf[0] != 0x28 {
		return fmt.Errorf("invalid handshake response: got %d bytes, first byte 0x%02X", n, buf[0])
	}

	// Session ID is at bytes 19-20
	c.sessionID[0] = buf[19]
	c.sessionID[1] = buf[20]
	return nil
}

// nextSeq returns the next sequence number (1–255, wrapping).
// Since we use fresh connect+handshake per command, only one command is ever
// in flight — the seq matching in ACK is effectively redundant. We still
// increment to follow the protocol convention used by other Mi-Light libraries.
func (c *UDPClient) nextSeq() byte {
	c.seqNum++
	if c.seqNum == 0 {
		c.seqNum = 1
	}
	return c.seqNum
}

// SendCommand sends a 9-byte command to a specific zone and waits for ACK.
// Returns nil on success, ErrNoACK if no ACK received.
func (c *UDPClient) SendCommand(cmd [9]byte, zone byte, ackTimeout time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	seq := c.nextSeq()
	packet := c.buildFrame(cmd, zone, seq)

	logging.Debug("Mi-Light TX", "hex", hex.EncodeToString(packet), "seq", seq, "zone", zone)

	if err := c.conn.SetWriteDeadline(time.Now().Add(1 * time.Second)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if _, err := c.conn.Write(packet); err != nil {
		return fmt.Errorf("send command: %w", err)
	}

	// Wait for ACK (0x88 with matching sequence number)
	buf := make([]byte, 64)
	if err := c.conn.SetReadDeadline(time.Now().Add(ackTimeout)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	n, err := c.conn.Read(buf)
	if err != nil {
		return ErrNoACK
	}

	logging.Debug("Mi-Light RX", "hex", hex.EncodeToString(buf[:n]), "expectSeq", seq)

	if n >= 7 && buf[0] == 0x88 && buf[6] == seq {
		return nil
	}
	return ErrNoACK
}

// ErrNoACK indicates the iBox2 did not acknowledge the command.
var ErrNoACK = fmt.Errorf("no ACK from iBox2")

// Close closes the UDP connection.
func (c *UDPClient) Close() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// buildFrame constructs the 22-byte v6 command frame.
//
// Frame layout:
//
//	80 00 00 00 11 {WB1 WB2} 00 {SeqNum} 00 {CMD[9]} {Zone} 00 {Checksum}
func (c *UDPClient) buildFrame(cmd [9]byte, zone byte, seq byte) []byte {
	frame := make([]byte, 22)
	frame[0] = 0x80
	frame[1] = 0x00
	frame[2] = 0x00
	frame[3] = 0x00
	frame[4] = 0x11
	frame[5] = c.sessionID[0]
	frame[6] = c.sessionID[1]
	frame[7] = 0x00
	frame[8] = seq
	frame[9] = 0x00
	copy(frame[10:19], cmd[:])
	frame[19] = zone
	frame[20] = 0x00

	// Checksum: sum of bytes 10–20, mod 256
	var sum byte
	for i := 10; i <= 20; i++ {
		sum += frame[i]
	}
	frame[21] = sum

	return frame
}

