package poller

import (
	"fmt"
	"time"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/uhn"
)

type CommandScheduler interface {
//		ScheduleAfter(deviceName string, key string, cmd DeviceCommand, d time.Duration, opts ScheduleOptions) (string, error)
//	ScheduleAt(deviceName string, key string, cmd DeviceCommand, t time.Time, opts ScheduleOptions) (string, error)
//	ScheduleEvery(deviceName string, key string, cmd DeviceCommand, interval time.Duration, opts ScheduleOptions) (string, error)

	Schedule(cmd uhn.DeviceCommand, delay time.Duration) (id string, err error)
	SchedulePulse(cmd uhn.DeviceCommand, delay time.Duration) (err error)
	ClearPulse(cmd uhn.DeviceCommand) bool
	Cancel(id string) bool
	Stop()
}

type commandScheduler struct {
	timers        map[string]*time.Timer
	pulses        map[string]*time.Timer
	commandPusher uhn.CommandPusher
}

func NewCommandScheduler(pusher uhn.CommandPusher) CommandScheduler {
	logging.Debug("Command scheduler created")
	return &commandScheduler{
		timers:        make(map[string]*time.Timer),
		pulses:        make(map[string]*time.Timer),
		commandPusher: pusher,
	}
}

func (cs *commandScheduler) Schedule(cmd uhn.DeviceCommand, delay time.Duration) (string, error) {
	if delay == 0 {
		cs.commandPusher.PushCommand(cmd)
		return "", nil
	}
	id := cmd.ID
	if id == "" {

		id = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	timer := time.AfterFunc(delay, func() {
		cs.commandPusher.PushCommand(cmd)
	})

	cs.timers[id] = timer
	return id, nil
}

func (cs *commandScheduler) SchedulePulse(cmd uhn.DeviceCommand, delay time.Duration) error {
	if delay == 0 {
		cs.commandPusher.PushCommand(cmd)
		return nil
	}

	timer := time.AfterFunc(delay, func() {
		cs.commandPusher.PushCommand(cmd)
	})

	cs.pulses[cmd.Device.Name] = timer
	return nil
}

func (cs *commandScheduler) ClearPulse(cmd uhn.DeviceCommand) bool {
	if timer, exists := cs.pulses[cmd.Device.Name]; exists {
		timer.Stop()
		delete(cs.pulses, cmd.Device.Name)
		return true
	}
	return false

}
func (cs *commandScheduler) Cancel(id string) bool {
	if timer, exists := cs.timers[id]; exists {
		timer.Stop()
		delete(cs.timers, id)
		return true
	}
	return false
}

func (cs *commandScheduler) Stop() {
	for id, timer := range cs.timers {
		timer.Stop()
		delete(cs.timers, id)
	}
	logging.Debug("Command scheduler stopped")
}
