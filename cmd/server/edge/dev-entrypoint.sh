#!/usr/bin/env sh
set -e


echo "🔧 Creating virtual serial ports with socat..."

SOCAT_LOG=$(mktemp)


#socat -d -d pty,raw,echo=0,link=/dev/ttyV0 pty,raw,echo=0,link=/dev/ttyV0s &
#socat  pty,raw,echo=0,link=/dev/ttyV0 pty,raw,echo=0,link=/dev/ttyV0s &
#socat -d -d pty,raw,echo=0 pty,raw,echo=0 &
## -d -d print the the created PTY names
socat -d -d pty,raw,echo=0 pty,raw,echo=0 2>"$SOCAT_LOG" &

SOCAT_PID=$!

while [ ! -s "$SOCAT_LOG" ]; do
    sleep 0.5
done

cat $SOCAT_LOG

PORTS=$(grep -o '/dev/pts/[0-9]\+' "$SOCAT_LOG")

export EDGE_PORT=$(echo "$PORTS" | sed -n 1p)
export SIM_PORT=$(echo "$PORTS" | sed -n 2p)

echo "🧩 Edge connected to: $EDGE_PORT"
echo "🧩 Simulator listening on: $SIM_PORT"

# Build edge config with the actual PTY port for bus_a
jq --arg port "$EDGE_PORT" '.buses[0].port = $port' /config/edge-config-dev.json > /config/edge-config.json

# Sim uses the other end of the PTY pair
jq --arg port "$SIM_PORT" '.buses[0].port = $port' /config/edge-config-dev.json > /config/sim-config.json

export SIM_CONFIG_PATH=/config/sim-config.json
# Wait a moment so PTYs exist
sleep 0.5

# Start modbus simulator (handles both RTU + TCP buses)
/mb-sim &

# Run edge (foreground)
/edge
