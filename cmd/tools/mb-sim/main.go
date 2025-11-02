package main

// cSpell:ignore mbserver Modbus
import (
	"log"
	//"net"
	"os"
	"time"

	"github.com/tbrandon/mbserver"
)

func main() {
	addr := os.Getenv("MB_LISTEN_ADDR")
	if addr == "" {
		addr = ":1502"
	}

	srv := mbserver.NewServer()
	// Seed a few registers/coils
	//srv.HoldingRegisters[0] = 123 // HR40001
	//srv.HoldingRegisters[1] = 456 // HR40002
	//srv.InputRegisters[0] = 321   // IR30001
	srv.Coils[0] = 1          // C00001
	srv.Coils[1] = 1          // C00001
	srv.Coils[2] = 0          // C00001
	srv.Coils[3] = 0          // C00001
	srv.DiscreteInputs[0] = 0 // DI10001

	/*_, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}*/
	if err := srv.ListenTCP(addr); err != nil {
		log.Fatalf("ListenTCP: %v", err)
	}
	defer srv.Close()
	log.Printf("Modbus TCP slave listening on %s", addr)
	// Wait forever
	for {
		time.Sleep(1 * time.Second)
	}
}
