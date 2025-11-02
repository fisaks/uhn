package modbus

// cSpell:ignore MQTTCli modbus mqtt
import (
	"context"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/goburrow/modbus"
)

type Poller struct {
	MQTTCli     mqtt.Client
	TCPAddr     string
	Period      time.Duration
	UnitID      byte
	TopicPrefix string // e.g. "edge/dev1"
}

func (p *Poller) Start(ctx context.Context) error {

	handler := modbus.NewTCPClientHandler(p.TCPAddr)
	handler.Timeout = 2 * time.Second
	handler.SlaveId = p.UnitID

	if err := handler.Connect(); err != nil {
		return fmt.Errorf("modbus connect: %w", err)
	}
	defer handler.Close()

	client := modbus.NewClient(handler)
	ticker := time.NewTicker(p.Period)
	defer ticker.Stop()

	for {
		if err := p.pollOnce(client); err != nil {
			log.Printf("poll error: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (p *Poller) pollOnce(c modbus.Client) error {
	// Read some points (adjust as needed)
	// 1) Coils (00001…): FC1
	if res, err := c.ReadCoils(0, 4); err == nil {
		p.publish("coils", res)
	} else {
		return err
	}
	// 2) Discrete inputs (10001…): FC2
	if res, err := c.ReadDiscreteInputs(0, 4); err == nil {
		p.publish("discrete_inputs", res)
	}
	// 3) Input registers (30001…): FC4
	/*if res, err := c.ReadInputRegisters(0, 4); err == nil {
		p.publish("input_registers", res)
	}
	// 4) Holding registers (40001…): FC3
	if res, err := c.ReadHoldingRegisters(0, 4); err == nil {
		p.publish("holding_registers", res)
	}*/
	return nil
}

func (p *Poller) publish(kind string, payload []byte) {
	topic := fmt.Sprintf("%s/%s/u%d", p.TopicPrefix, kind, p.UnitID)
	p.MQTTCli.Publish(topic, 0, true, payload)
}
