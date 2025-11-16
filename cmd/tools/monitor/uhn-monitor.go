package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fisaks/uhn/internal/catalog"
)

type DeviceInfo struct {
	DigitalInputsCount  uint16
	DigitalOutputsCount uint16
	AnalogInputsCount   uint16
	AnalogOutputsCount  uint16
}

var deviceMap = map[string]DeviceInfo{}

func readCatalogMessage(payload []byte) (string, error) {
	var catalogMsg catalog.EdgeCatalogMessage

	// ... in message handler for uhn/catalog/* ...
	if err := json.Unmarshal(payload, &catalogMsg); err == nil {
		for _, dev := range catalogMsg.Devices {
			deviceInfo := DeviceInfo{}
			if dev.DigitalInputs != nil {
				deviceInfo.DigitalInputsCount = dev.DigitalInputs.Count
			}
			if dev.DigitalOutputs != nil {
				deviceInfo.DigitalOutputsCount = dev.DigitalOutputs.Count
			}
			if dev.AnalogInputs != nil {
				deviceInfo.AnalogInputsCount = dev.AnalogInputs.Count
			}
			if dev.AnalogOutputs != nil {
				deviceInfo.AnalogOutputsCount = dev.AnalogOutputs.Count
			}
			deviceMap[dev.Name] = deviceInfo
		}
	}
	out, err := json.Marshal(catalogMsg)
	return string(out), err

}

func decodeBitsField(field string, count uint16) string {
	raw, err := base64.StdEncoding.DecodeString(field)
	if err != nil {
		return fmt.Sprintf("(base64 error: %v)", err)
	}
	bits := make([]byte, count)
	bitIdx := 0
	for _, b := range raw {
		for bitPosition := 0; bitPosition < 8 && bitIdx < int(count); bitPosition++ {
			if b&(1<<uint(bitPosition)) != 0 {
				bits[int(count)-1-bitIdx] = '1'
			} else {
				bits[int(count)-1-bitIdx] = '0'
			}
			bitIdx++
		}
	}
	return string(bits)
}

func decodeUint16Field(field string) []uint16 {
	raw, err := base64.StdEncoding.DecodeString(field)
	if err != nil {
		return []uint16{}
	}
	out := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		word := uint16(raw[i])<<8 | uint16(raw[i+1])
		out = append(out, word)
	}
	return out
}

func processJSONReplaceBase64(payload []byte) (string, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal(payload, &obj); err != nil {
		// Not a JSON object; print as is
		return string(payload), nil
	}

	// Replace digital/analog fields if present and base64-looking
	for _, field := range []struct {
		Key       string
		IsDigital bool
	}{
		{"digitalOutputs", true},
		{"digitalInputs", true},
		{"analogOutputs", false},
		{"analogInputs", false},
	} {
		val, ok := obj[field.Key]
		name := obj["name"]
		valstr, isStr := val.(string)
		if ok && isStr && valstr != "" {
			if field.IsDigital {
				if field.Key == "digitalOutputs" {
					obj[field.Key] = decodeBitsField(valstr, deviceMap[name.(string)].DigitalOutputsCount)
				} else {
					obj[field.Key] = decodeBitsField(valstr, deviceMap[name.(string)].DigitalInputsCount)
				}
			} else {
				obj[field.Key] = decodeUint16Field(valstr)
			}
		}
	}

	// Compact, one-line JSON output
	out, err := json.Marshal(obj)
	return string(out), err
}

func main() {
	var broker, topic string
	flag.StringVar(&broker, "broker", "tcp://localhost:1883", "MQTT broker address")
	flag.StringVar(&topic, "topic", "uhn/#", "MQTT topic filter")
	flag.Parse()

	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID(fmt.Sprintf("uhn-monitor-%d", time.Now().UnixNano()))
	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		payload := msg.Payload()
		topic := msg.Topic()
		if strings.HasSuffix(topic, "catalog") {
			line, err := readCatalogMessage(payload)
			if err != nil {
				fmt.Printf("%s %s (error: %v)\n", topic, string(payload), err)
				return
			}
			fmt.Printf("%s %s\n", topic, line)
			return
		}

		line, err := processJSONReplaceBase64(payload)
		if err != nil {
			fmt.Printf("%s %s (error: %v)\n", topic, string(payload), err)
			return
		}
		fmt.Printf("%s %s\n", topic, line)
	})

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}
	fmt.Printf("Connected to MQTT broker %s, subscribing to %s...\n", broker, topic)

	if token := client.Subscribe(topic, 0, nil); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

	// Wait for interrupt
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()
	<-ctx.Done()
	client.Disconnect(200)
}
