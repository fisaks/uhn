package simulator

import (
	"io"
	"log"
	"net"
	"sync"
)

// tcpProxy is a lightweight TCP proxy that forwards connections from
// a listen address to a backend address. It tracks all active connections
// so they can be forcefully closed for chaos testing.
//
// The mbserver always listens on the backend address and is never stopped.
// The proxy controls whether the edge can reach it.
type tcpProxy struct {
	listenAddr string
	backendAddr string

	mu       sync.Mutex
	listener net.Listener
	conns    []net.Conn
	running  bool
}

func newTCPProxy(listenAddr, backendAddr string) *tcpProxy {
	return &tcpProxy{
		listenAddr:  listenAddr,
		backendAddr: backendAddr,
	}
}

func (p *tcpProxy) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return err
	}
	p.listener = ln
	p.running = true
	go p.acceptLoop()
	return nil
}

func (p *tcpProxy) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	p.listener.Close()
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = nil
	p.running = false
}

func (p *tcpProxy) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *tcpProxy) acceptLoop() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return // listener closed
		}
		go p.relay(client)
	}
}

func (p *tcpProxy) relay(client net.Conn) {
	if !p.isRunning() {
		client.Close()
		return
	}

	backend, err := net.Dial("tcp", p.backendAddr)
	if err != nil {
		log.Printf("TCP proxy: failed to dial backend %s: %v", p.backendAddr, err)
		client.Close()
		return
	}

	// Track both connections for cleanup on stop()
	p.mu.Lock()
	p.conns = append(p.conns, client, backend)
	p.mu.Unlock()

	// Bidirectional copy — when one direction gets an error,
	// close the other side to unblock its io.Copy
	go func() {
		io.Copy(backend, client)
		backend.Close()
	}()
	io.Copy(client, backend)
	client.Close()
}
