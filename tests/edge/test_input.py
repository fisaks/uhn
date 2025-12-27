import random
from dsl.fluent_multi import given

def test_setting_input_bit_on(rtu_sim_client, mqtt_watcher,di_test16_in):
    
    bit = 13
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(di_test16_in).and_input(bit).is_off().done()
            .resync() 
            .wait_for_expected_initial_states()
                .when_input_is_set_on(di_test16_in, bit)
                .thenDeviceInputBitIs(di_test16_in, bit, 1, timeout=3.0)
    )
def test_setting_input_bit_off(rtu_sim_client, mqtt_watcher,di_test16_in):
    
    bit = 7
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(di_test16_in).and_input(bit).is_on().done()
            .resync() 
            .wait_for_expected_initial_states()
                .when_input_is_set_off(di_test16_in, bit)
                .thenDeviceInputBitIs(di_test16_in, bit, 0, timeout=3.0)
    )

def test_when_something_external_change_input(rtu_sim_client, mqtt_watcher,kitchen_io8_1):
    
    bit = random.randint(0, 7)
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(kitchen_io8_1).and_input(bit).is_off().done()
            .resync() 
            .wait_for_expected_initial_states()
                .when_input_is_set_on(kitchen_io8_1, bit)
                .thenDeviceInputBitIs(kitchen_io8_1, bit, 1, timeout=3.0)
                .when_input_is_set_off(kitchen_io8_1, bit)
                .thenDeviceInputBitIs(kitchen_io8_1, bit, 0, timeout=3.0)

    )

def test_when_multiple_inputs_change_at_the_same_time(rtu_sim_client, mqtt_watcher,kitchen_io8_1,di_test16_in):
    
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(kitchen_io8_1).with_inputs([0]*8).done()
        .device(di_test16_in).with_inputs([0]*16).done()
            .resync() 
            .wait_for_expected_initial_states()
                .when_input_is_set_on(kitchen_io8_1, 2)
                .when_input_is_set_on(di_test16_in, 9)
                .when_input_is_set_on(kitchen_io8_1, 4)
                .when_input_is_set_on(di_test16_in, 12)
                .when_input_is_set_on(di_test16_in, 1)
                .thenDeviceInputIs(kitchen_io8_1, [0,0,0,1,0,1,0,0], timeout=3.0)
                .thenDeviceInputIs(di_test16_in, [0,0,0,1,0,0,1,0,  0,0,0,0,0,0,1,0], timeout=3.0)

    )
def test_toggle_input_bit(rtu_sim_client, mqtt_watcher,di_test16_in):
    
    bit = random.randint(0, 15)
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(di_test16_in).and_input(bit).is_off().done()
            .resync() 
            .wait_for_expected_initial_states()
                .when_input_toggled(di_test16_in, bit)
                .thenDeviceInputBitIs(di_test16_in, bit, 1, timeout=3.0)
                .when_input_toggled(di_test16_in, bit)
                .thenDeviceInputBitIs(di_test16_in, bit, 0, timeout=3.0)
    )
