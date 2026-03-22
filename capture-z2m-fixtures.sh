#!/bin/bash
# Capture Z2M device definitions and state for the Zigbee simulator.
# Run this when Z2M is running with real hardware to update sim fixtures.
#
# Usage: ./capture-z2m-fixtures.sh
#
# Output:
#   config/zigbee-sim-devices.json  — bridge/devices (sanitized)
#   config/zigbee-sim-state.json    — current device states (sanitized)
#
# Sanitization removes: coordinator, real IEEE addresses, endpoints,
# interview details, firmware update URLs, device-specific config,
# energy accumulators.

set -e
cd "$(dirname "$0")"

echo "Capturing bridge/devices..."
mosquitto_sub -t 'zigbee2mqtt/bridge/devices' -C 1 > /tmp/z2m-raw-devices.json
echo "  Captured raw devices"

echo "Capturing device states..."
python3 -c "
import json, subprocess, sys

with open('/tmp/z2m-raw-devices.json') as f:
    devices = json.load(f)

state = {}
for d in devices:
    name = d.get('friendly_name', '')
    if d.get('type') == 'Coordinator':
        continue
    sys.stdout.write(f'  {name}...')
    sys.stdout.flush()
    try:
        result = subprocess.run(
            ['mosquitto_sub', '-t', f'zigbee2mqtt/{name}', '-C', '1', '-W', '5'],
            capture_output=True, text=True, timeout=8
        )
        if result.stdout.strip():
            state[name] = json.loads(result.stdout)
            print(f' {len(state[name])} properties')
        else:
            state[name] = {}
            print(' no retained state (battery device?)')
    except Exception as e:
        state[name] = {}
        print(f' error: {e}')

with open('/tmp/z2m-raw-state.json', 'w') as f:
    json.dump(state, f, indent=2)
print(f'  Captured {len(state)} devices')
"

echo "Sanitizing..."
python3 -c "
import json

# --- Sanitize devices ---
with open('/tmp/z2m-raw-devices.json') as f:
    devices = json.load(f)

# Generate fake IEEE addresses
counter = 1
sanitized = []
for d in devices:
    if d.get('type') == 'Coordinator':
        continue
    # Replace real IEEE with fake
    d['ieee_address'] = f'0x00158d0001abcde{counter}'
    counter += 1
    # Strip internal Zigbee details
    for key in ['endpoints', 'interview_completed', 'interview_state',
                'interviewing', 'network_address', 'date_code',
                'model_id', 'power_source', 'software_build_id']:
        d.pop(key, None)
    sanitized.append(d)

with open('config/zigbee-sim-devices.json', 'w') as f:
    json.dump(sanitized, f, indent=2)
print(f'  Devices: {len(sanitized)} (coordinator removed, addresses sanitized)')

# --- Sanitize state ---
with open('/tmp/z2m-raw-state.json') as f:
    state = json.load(f)

for name, props in state.items():
    for key in ['update', 'inching_control_set', 'overload_protection',
                'outlet_control_protect', 'power_on_behavior', 'last_seen',
                'energy', 'energy_month', 'energy_today', 'energy_yesterday']:
        props.pop(key, None)

with open('config/zigbee-sim-state.json', 'w') as f:
    json.dump(state, f, indent=2)
print(f'  State: {len(state)} devices (config/internal fields removed)')
"

# Cleanup temp files
rm -f /tmp/z2m-raw-devices.json /tmp/z2m-raw-state.json

echo "Done. Devices without retained state need manual defaults in zigbee-sim-state.json."
echo "Review and commit the fixture files."
