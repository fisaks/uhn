import random
from dsl.fluent_multi import given

def test_button_pressed_long(rtu_sim_client, mqtt_watcher,io_kitchen,):
    
    bit = random.randint(0, 7)
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(io_kitchen).and_output(bit).is_off().done()
            .resync() 
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_button_pressed_long(io_kitchen, bit)
                .sequence()
                    .input(io_kitchen,bit,True)
                    .between(1.5,2.5)
                    .input(io_kitchen,bit,False)
                    .verify(timeout=6.0)
    )


def test_button_pressed_short(rtu_sim_client, mqtt_watcher,io_kitchen,):
    
    bit = random.randint(0, 7)
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(io_kitchen).and_output(bit).is_off().done()
            .resync() 
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_button_pressed_short(io_kitchen, bit)
                .sequence()
                    .input(io_kitchen,bit,True)
                    .between(0.5,1.5)
                    .input(io_kitchen,bit,False)
                    .verify(timeout=6.0)
    )

def test_button_tapped(rtu_sim_client, mqtt_watcher,io_kitchen,):
    
    bit = random.randint(0, 7)
    s = given(rtu_sim_client, mqtt_watcher)

    (
        s.device(io_kitchen).and_output(bit).is_off().done()
            .resync() 
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_button_tapped(io_kitchen, bit)
                .sequence()
                    .input(io_kitchen,bit,True)
                    .between(0.25,0.75)
                    .input(io_kitchen,bit,False)
                    .verify(timeout=6.0)
    )

    

