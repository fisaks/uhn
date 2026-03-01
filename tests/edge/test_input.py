import random
from dsl.fluent_multi import given

def test_setting_input_bit_on(env, mqtt_watcher):

    bit = 13
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.di16_in).and_input(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_input_is_set_on(env.di16_in, bit)
                .thenDeviceInputBitIs(env.di16_in, bit, 1, timeout=3.0)
    )
def test_setting_input_bit_off(env, mqtt_watcher):

    bit = 7
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.di16_in).and_input(bit).is_on().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_input_is_set_off(env.di16_in, bit)
                .thenDeviceInputBitIs(env.di16_in, bit, 0, timeout=3.0)
    )

def test_when_something_external_change_input(env, mqtt_watcher):

    bit = random.randint(0, 7)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io8_1).and_input(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_input_is_set_on(env.io8_1, bit)
                .thenDeviceInputBitIs(env.io8_1, bit, 1, timeout=3.0)
                .when_input_is_set_off(env.io8_1, bit)
                .thenDeviceInputBitIs(env.io8_1, bit, 0, timeout=3.0)

    )

def test_when_multiple_inputs_change_at_the_same_time(env, mqtt_watcher):

    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io8_1).with_inputs([0]*8).done()
        .device(env.di16_in).with_inputs([0]*16).done()
            .resync()
            .wait_for_expected_initial_states()
                .when_input_is_set_on(env.io8_1, 2)
                .when_input_is_set_on(env.di16_in, 9)
                .when_input_is_set_on(env.io8_1, 4)
                .when_input_is_set_on(env.di16_in, 12)
                .when_input_is_set_on(env.di16_in, 1)
                .thenDeviceInputIs(env.io8_1, [0,0,0,1,0,1,0,0], timeout=3.0)
                .thenDeviceInputIs(env.di16_in, [0,0,0,1,0,0,1,0,  0,0,0,0,0,0,1,0], timeout=3.0)

    )
def test_toggle_input_bit(env, mqtt_watcher):

    bit = random.randint(0, 15)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.di16_in).and_input(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
                .when_input_toggled(env.di16_in, bit)
                .thenDeviceInputBitIs(env.di16_in, bit, 1, timeout=3.0)
                .when_input_toggled(env.di16_in, bit)
                .thenDeviceInputBitIs(env.di16_in, bit, 0, timeout=3.0)
    )
