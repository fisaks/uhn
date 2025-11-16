package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fisaks/uhn/internal/uhn"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  uhnctl push --edge EDGE --device DEVICE --address ADDRESS --value VALUE

Required flags for 'push':
  --edge     (string)   Name of the edge
  --device   (string)   Name of the device
  --address  (int)      Address of the device (integer)
  --value    (int)      Value to send (integer)
Optional flags for 'push':  
  --pulse    (int)      Pulse duration in milliseconds (default: 0)

  Optional flags:
  --broker   (string)   MQTT broker address (default: tcp://localhost:1883)
  

`)
	flag.PrintDefaults()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Missing command (e.g. push)\n")
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]

	// Only support "push" for now
	if cmd != "push" {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		usage()
		os.Exit(2)
	}

	// Define flags for push
	pushFlags := flag.NewFlagSet("push", flag.ExitOnError)
	edge := pushFlags.String("edge", "", "Edge name (required)")
	device := pushFlags.String("device", "", "Device name (required)")
	address := pushFlags.Int("address", -1, "Device address (required)")
	value := pushFlags.Int("value", -1, "Value to send (required)")
	pulse := pushFlags.Int("pulse", 0, "Pulse duration in milliseconds (optional)")
	broker := pushFlags.String("broker", "tcp://localhost:1883", "MQTT broker address")

	pushFlags.Usage = usage

	// Parse flags after "push"
	if err := pushFlags.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	// Check required flags
	missing := false
	if *edge == "" {
		fmt.Fprintf(os.Stderr, "--edge is required\n")
		missing = true
	}
	if *device == "" {
		fmt.Fprintf(os.Stderr, "--device is required\n")
		missing = true
	}
	if *address < 0 {
		fmt.Fprintf(os.Stderr, "--address is required and must be >= 0\n")
		missing = true
	}
	if *value < 0 {
		fmt.Fprintf(os.Stderr, "--value is required and must be >= 0\n")
		missing = true
	}
	if missing {
		usage()
		os.Exit(2)
	}

	// TODO: Perform the push action
	fmt.Printf("Pushing value %d to device %s on edge %s (address %d)\n", *value, *device, *edge, *address)
	// ... actual logic here ...
	opts := mqtt.NewClientOptions()
	opts.AddBroker(*broker)
	opts.SetClientID(fmt.Sprintf("uhnctl-%d", time.Now().UnixNano()))
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		fmt.Fprintf(os.Stderr, "MQTT connect error: %v\n", token.Error())
		os.Exit(1)
	}
	defer client.Disconnect(250)

	topic := fmt.Sprintf("uhn/%s/device/%s/cmd", *edge, *device)
	payload := uhn.IncomingDeviceCommand{
		Address: *address,
		Value:   *value,
		Action:  "setDigitalOutput",
		Device:  *device,
	}
	if *pulse > 0 {
		payload.PulseMs = *pulse
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON marshal error: %v\n", err)
		os.Exit(1)
	}
	token := client.Publish(topic, 0, false, payloadBytes)
	token.Wait()
	if token.Error() != nil {
		fmt.Fprintf(os.Stderr, "MQTT publish error: %v\n", token.Error())
		os.Exit(1)
	}

	fmt.Println("Push command published successfully")
}
