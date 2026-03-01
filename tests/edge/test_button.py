import random
from dsl.fluent_multi import given

def test_button_pressed_long(env, mqtt_watcher):

    bit = random.randint(0, 7)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io8_1).and_output(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_button_pressed_long(env.io8_1, bit)
                .sequence()
                    .input(env.io8_1,bit,True)
                    .between(1.5,2.5)
                    .input(env.io8_1,bit,False)
                    .verify(timeout=6.0)
    )


def test_button_pressed_short(env, mqtt_watcher):

    bit = random.randint(0, 7)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io8_1).and_output(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_button_pressed_short(env.io8_1, bit)
                .sequence()
                    .input(env.io8_1,bit,True)
                    .between(0.5,1.5)
                    .input(env.io8_1,bit,False)
                    .verify(timeout=6.0)
    )

def test_button_tapped(env, mqtt_watcher):

    bit = random.randint(0, 7)
    s = given(env.sim, mqtt_watcher)

    (
        s.device(env.io8_1).and_output(bit).is_off().done()
            .resync()
            .wait_for_expected_initial_states()
            .clear_message_log()
            .log_state_messages(True)
                .when_button_tapped(env.io8_1, bit)
                .sequence()
                    .input(env.io8_1,bit,True)
                    .between(0.25,0.75)
                    .input(env.io8_1,bit,False)
                    .verify(timeout=6.0)
    )
