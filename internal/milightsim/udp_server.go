package milightsim

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

// UDPServer simulates an iBox2 Mi-Light bridge on UDP.
type UDPServer struct {
	store   *MilightSimStore
	port    int
	conn    *net.UDPConn
	running atomic.Bool
	mu      sync.Mutex
	stopCh  chan struct{}
}

// NewUDPServer creates a UDP simulator server.
func NewUDPServer(store *MilightSimStore, port int) *UDPServer {
	return &UDPServer{
		store:  store,
		port:   port,
		stopCh: make(chan struct{}),
	}
}

// Start begins listening for UDP v6 packets. Blocks until stopped.
func (s *UDPServer) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("resolve addr: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen UDP: %w", err)
	}

	s.mu.Lock()
	s.conn = conn
	s.running.Store(true)
	s.mu.Unlock()

	log.Printf("Mi-Light Sim UDP listening on :%d", s.port)

	buf := make([]byte, 128)
	for {
		select {
		case <-s.stopCh:
			return nil
		default:
		}

		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if !s.running.Load() {
				return nil // stopped
			}
			log.Printf("Mi-Light Sim UDP read error: %v", err)
			continue
		}

		if n == 0 {
			continue
		}

		s.handlePacket(conn, remoteAddr, buf[:n])
	}
}

// Stop stops the UDP server.
func (s *UDPServer) Stop() {
	s.running.Store(false)
	close(s.stopCh)
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	s.mu.Unlock()
}

// Restart restarts the UDP server (for outage simulation).
func (s *UDPServer) Restart() error {
	s.stopCh = make(chan struct{})
	go s.Start()
	return nil
}

// IsRunning returns whether the server is accepting connections.
func (s *UDPServer) IsRunning() bool {
	return s.running.Load()
}

func (s *UDPServer) handlePacket(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	if len(data) == 0 {
		return
	}

	switch data[0] {
	case 0x20:
		// Handshake request
		s.handleHandshake(conn, addr, data)
	case 0x80:
		// Command
		s.handleCommand(conn, addr, data)
	case 0xD0:
		// Keep-alive
		s.handleKeepAlive(conn, addr, data)
	default:
		log.Printf("Mi-Light Sim: unknown packet type 0x%02X from %s (%d bytes)", data[0], addr, len(data))
	}
}

func (s *UDPServer) handleHandshake(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	log.Printf("Mi-Light Sim: handshake from %s", addr)

	// Respond with 0x28 packet (22 bytes minimum)
	// Session ID at bytes 19-20: use fixed sim values
	resp := make([]byte, 22)
	resp[0] = 0x28
	resp[19] = 0xAA // session ID byte 1
	resp[20] = 0xBB // session ID byte 2

	conn.WriteToUDP(resp, addr)
}

func (s *UDPServer) handleCommand(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	if len(data) < 22 {
		log.Printf("Mi-Light Sim: short command packet (%d bytes) from %s", len(data), addr)
		return
	}

	seq := data[8]
	zone := data[19]

	// Parse the 9-byte command payload (bytes 10-18)
	cmdType := data[14] // command type byte within the 9-byte payload
	cmdValue := data[15]

	switch cmdType {
	case 0x04: // power / night mode / mode speed
		switch cmdValue {
		case 0x01:
			s.store.SetPower(zone, true)
			log.Printf("Mi-Light Sim: zone %d power ON", zone)
		case 0x02:
			s.store.SetPower(zone, false)
			log.Printf("Mi-Light Sim: zone %d power OFF", zone)
		case 0x03:
			s.store.IncModeSpeed(zone)
			log.Printf("Mi-Light Sim: zone %d mode speed UP", zone)
		case 0x04:
			s.store.DecModeSpeed(zone)
			log.Printf("Mi-Light Sim: zone %d mode speed DOWN", zone)
		case 0x05:
			log.Printf("Mi-Light Sim: zone %d night mode", zone)
		}
	case 0x03: // brightness
		s.store.SetBrightness(zone, int(cmdValue))
		log.Printf("Mi-Light Sim: zone %d brightness %d", zone, cmdValue)
	case 0x02: // saturation
		s.store.SetSaturation(zone, int(cmdValue))
		log.Printf("Mi-Light Sim: zone %d saturation %d", zone, cmdValue)
	case 0x01: // hue (enters color mode)
		s.store.SetHue(zone, int(cmdValue))
		log.Printf("Mi-Light Sim: zone %d hue %d", zone, cmdValue)
	case 0x05: // color temperature (0=warm, 100=cool; 0x64=white mode)
		s.store.SetColorTemp(zone, int(cmdValue))
		log.Printf("Mi-Light Sim: zone %d colorTemp %d", zone, cmdValue)
	case 0x06: // mode/effect
		s.store.SetMode(zone, int(cmdValue))
		log.Printf("Mi-Light Sim: zone %d mode %d", zone, cmdValue)
	default:
		log.Printf("Mi-Light Sim: zone %d unknown cmd type 0x%02X value %d", zone, cmdType, cmdValue)
	}

	// Send ACK: 88 00 00 00 03 00 {SeqNum}
	ack := []byte{0x88, 0x00, 0x00, 0x00, 0x03, 0x00, seq}
	conn.WriteToUDP(ack, addr)
}

func (s *UDPServer) handleKeepAlive(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	// Respond with D8 packet
	resp := []byte{0xD8, 0x00, 0x00, 0x00, 0x07, 0x00, 0x00}
	conn.WriteToUDP(resp, addr)
}
