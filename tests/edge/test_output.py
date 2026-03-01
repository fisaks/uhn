import random
from dsl.fluent_multi import given

def test_setting_output_bit_on(env, mqtt_watcher):

    bit = 13
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io16_out).and_output(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_command(env.io16_out, action="setdigitaloutput", address=bit, value=1)
                .thenDeviceOutputBitIs(env.io16_out, bit, 1, timeout=8.0)
    )


def test_setting_output_bit_off(env, mqtt_watcher):

    bit = 7
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io16_out).and_output(bit).is_on().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_command(env.io16_out, action="setdigitaloutput", address=bit, value=0)
                .thenDeviceOutputBitIs(env.io16_out, bit, 0, timeout=8.0)
    )

def test_when_something_external_change_output(env, mqtt_watcher):

    bit = random.randint(0, 7)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io8_1).and_output(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_output_is_set_on(env.io8_1, bit)
                .thenDeviceOutputBitIs(env.io8_1, bit, 1, timeout=3.0)
                .when_output_is_set_off(env.io8_1, bit)
                .thenDeviceOutputBitIs(env.io8_1, bit, 0, timeout=3.0)

    )

def test_when_multiple_outputs_change_at_the_same_time(env, mqtt_watcher):

    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io8_1).with_outputs([1]*8).done()
        .device(env.io16_out).with_outputs([1]*16).done()
            .resync()
            .wait_for_expected_initial_states()
                .when_output_is_set_off(env.io8_1, 2)
                .when_output_is_set_off(env.io16_out, 9)
                .when_command(env.io8_1, action="setdigitaloutput", address=4, value=0)
                .when_command(env.io16_out, action="setdigitaloutput", address=12, value=0)
                .when_command(env.io16_out, action="setdigitaloutput", address=1, value=0)
                .thenDeviceOutputIs(env.io8_1, [1,1,1,0,1,0,1,1], timeout=3.0)
                .thenDeviceOutputIs(env.io16_out, [1,1,1,0,1,1,0,1,  1,1,1,1,1,1,0,1], timeout=3.0)
    )

def test_toggle_output(env, mqtt_watcher):

    bit = random.randint(0, 15)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io16_out).and_output(bit).is_on().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_output_toggled(env.io16_out, bit)
                .thenDeviceOutputBitIs(env.io16_out, bit, 0, timeout=3.0)
                .when_output_toggled(env.io16_out, bit)
                .thenDeviceOutputBitIs(env.io16_out, bit, 1, timeout=3.0)
    )

def test_pulse_output(env, mqtt_watcher):

    bit = random.randint(0, 15)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io16_out).with_outputs([0]*16).done()
            .resync()
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_command(env.io16_out, action="setdigitaloutput", address=bit, value=1, pulseMs=500)
                .sequence()
                    .output(env.io16_out,bit,True)
                    .between(0.25,0.75)
                    .output(env.io16_out,bit,False)
                    .verify(timeout=6.0)
    )


def test_pulse_should_be_canceled_if_other_command_arrives(env, mqtt_watcher):

    bit = random.randint(0, 15)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io16_out).with_outputs([0]*16).done()
            .resync()
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_command(env.io16_out, action="setdigitaloutput", address=bit, value=1, pulseMs=500)
                .wait(0.5)# we wait the same time and send the same command without pulse to cancel the previous pulse
                .when_command(env.io16_out, action="setdigitaloutput", address=bit, value=1)
                .sequence()
                    .output(env.io16_out,bit,True)
                    .never()
                    .output(env.io16_out,bit,False)
                    .verify(timeout=3.0)
    )
