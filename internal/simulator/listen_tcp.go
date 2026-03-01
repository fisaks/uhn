package simulator

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"

	"github.com/fisaks/uhn/internal/config"
)

// TCPListenerControl manages TCP connectivity for chaos testing.
//
// Architecture: the mbserver always listens on an internal port and is never
// stopped. A lightweight TCP proxy sits between the edge and the mbserver on
// the configured (public) port. Stop/Start control the proxy only — stopping
// it forcefully closes all active connections so the edge detects the failure.
type TCPListenerControl struct {
	simStore *SimStore
	buses   []*config.BusConfig
	proxies map[string]*tcpProxy // busId → proxy
	mu      sync.Mutex
	running bool
}

// internalAddr returns an address with the port offset by +10000.
// e.g. "localhost:1502" → "localhost:11502"
func internalAddr(addr string) (string, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(port+10000)), nil
}

// StartListenTCP starts Modbus TCP listeners on internal ports and creates
// proxies on the configured (public) ports.
// Returns a TCPListenerControl for stop/start lifecycle management.
func StartListenTCP(simStore *SimStore, buses []*config.BusConfig) (*TCPListenerControl, error) {
	ctrl := &TCPListenerControl{
		simStore: simStore,
		buses:   buses,
		proxies: make(map[string]*tcpProxy),
	}

	for _, bus := range buses {
		if bus.Type != "tcp" {
			continue
		}

		srv := simStore.GetServer(bus.BusId)
		if srv == nil {
			log.Printf("TCP: no server for bus %s, skipping", bus.BusId)
			continue
		}

		// mbserver listens on the internal port (never stopped)
		intAddr, err := internalAddr(bus.TCPAddr)
		if err != nil {
			return nil, fmt.Errorf("invalid TCP address %s: %w", bus.TCPAddr, err)
		}
		if err := srv.ListenTCP(intAddr); err != nil {
			return nil, fmt.Errorf("ListenTCP on %s: %w", intAddr, err)
		}
		log.Printf("TCP simulator backend on %s for bus %s", intAddr, bus.BusId)

		// Proxy on the public port (what the edge connects to)
		proxy := newTCPProxy(bus.TCPAddr, intAddr)
		if err := proxy.start(); err != nil {
			return nil, fmt.Errorf("TCP proxy on %s: %w", bus.TCPAddr, err)
		}
		ctrl.proxies[bus.BusId] = proxy
		log.Printf("TCP proxy %s → %s for bus %s", bus.TCPAddr, intAddr, bus.BusId)
	}

	ctrl.running = true
	return ctrl, nil
}

// Stop closes all proxy connections and listeners.
// The edge will lose connectivity and detect errors.
// The mbserver continues running on the internal port.
func (c *TCPListenerControl) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return
	}

	for busId, proxy := range c.proxies {
		proxy.stop()
		log.Printf("TCP proxy stopped on bus %s", busId)
	}

	c.running = false
}

// Start re-opens proxy listeners so the edge can reconnect.
func (c *TCPListenerControl) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	for busId, proxy := range c.proxies {
		if err := proxy.start(); err != nil {
			return fmt.Errorf("TCP proxy restart on bus %s: %w", busId, err)
		}
		log.Printf("TCP proxy restarted on bus %s", busId)
	}

	c.running = true
	return nil
}

// IsRunning returns whether the TCP proxies are currently active.
func (c *TCPListenerControl) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}
