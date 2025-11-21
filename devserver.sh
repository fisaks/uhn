#!/bin/bash

SESSION="uhn-dev"
WORKDIR=$(pwd)

print_usage() {
    echo "Usage: $0 {start|stop}"
    echo "Commands:"
    echo "  start   Start the dev server in tmux"
    echo "  debug   Start dev server with headless dlv "
    echo "  stop    Stop the dev server"
}

start_dev_env() {
    echo "🔧 Starting development environment in tmux session '$SESSION'..."
    
    local debug="${1:-false}"   # default false
    if [[ "$debug" == "true" ]]; then
        echo "running in debug hot reload mode"
        EDGE_AIR_FILE=".air-dvl.toml"
        RTU_SIM_AIR_FILE=".air-rtu-dvl.toml"
    else
        echo "running in hot reload mode"
        EDGE_AIR_FILE=".air.toml"
        RTU_SIM_AIR_FILE=".air-rtu.toml"
    fi
    # Start Mosquitto container before anything else
    if ! docker ps --format '{{.Names}}' | grep -q '^uhn-mosquitto$$'; then
        echo "🐳 Starting Mosquitto via Docker Compose..."
        docker compose --profile dev up -d mosquitto
    else
        echo "✅ Mosquitto already running"
    fi
    
    echo "⏳ Waiting for Mosquitto to be ready on localhost:1883..."
    for i in {1..10}; do
        if nc -z localhost 1883; then
            echo "✅ Mosquitto is up!"
            break
        fi
        echo "  ...retrying ($i)"
        sleep 1
    done
    
    # Check if the session already exists
    if tmux has-session -t $SESSION 2>/dev/null; then
        echo "Session $SESSION already exists. Attaching to it..."
        tmux attach-session -t $SESSION
        exit 0
    fi
    
    echo "🔧 Creating virtual serial ports with socat..."
    
    SOCAT_LOG=$(mktemp)
    socat -d -d pty,raw,echo=0 pty,raw,echo=0 2>"$SOCAT_LOG" &
    SOCAT_PID=$!
    sleep 1
    PORTS=$(grep -o '/dev/pts/[0-9]\+' "$SOCAT_LOG")
    export EDGE_PORT=$(echo "$PORTS" | sed -n 1p)
    export SIM_PORT=$(echo "$PORTS" | sed -n 2p)
    
    # Write temporary config file with correct port
    export EDGE_CONFIG_PATH="tmp/edge-config-dev.json"
    export SIM_CONFIG_PATH="tmp/sim-config-dev.json"
    
    mkdir -p tmp
    rm -f "$EDGE_CONFIG_PATH" "$SIM_CONFIG_PATH"
    #jq --arg port "$EDGE_PORT" '.buses[0].port = $port' config/edge-config-dev.json > "$EDGE_CONFIG_TMP"
    jq --arg port "$SIM_PORT" '.buses[0].port = $port' config/edge-config-dev.json > "$SIM_CONFIG_TMP"
    export MQTT_URL=tcp://localhost:1883
    export EDGE_NAME=edge1
    export UHN_LOG_LEVEL=debug
    
    echo "🧩 Edge connected to: $EDGE_PORT"
    echo "🧩 RTU simulator listening on: $SIM_PORT"
    echo "📊 Log level: $UHN_LOG_LEVEL"
    echo "📡 MQTT: $MQTT_URL"
    echo "📄 Config: $EDGE_CONFIG_PATH / $SIM_CONFIG_PATH"
    echo "EDGE_PORT=$EDGE_PORT" > tmp/ports.env
    echo "SIM_PORT=$SIM_PORT" >> tmp/ports.env
    
    
    # Create tmux session and first pane: MQTT monitor
    tmux new-session -d -s $SESSION -n dev
    #tmux send-keys -t $SESSION.0 "mosquitto_sub -h localhost -t 'uhn/#' -v" C-m
    tmux send-keys -t $SESSION.0 "go build -o tmp/uhn-monitor ./cmd/tools/monitor && ./tmp/uhn-monitor" C-m
    
    # Split below (75% bottom), top remains MQTT monitor
    tmux split-window -v -t $SESSION.0
    tmux resize-pane -t $SESSION.0 -y 5
    tmux send-keys -t $SESSION.1 \
    "echo '🪵 Showing Mosquitto logs (press Ctrl-b d to detach)' && docker logs -f uhn-mosquitto" C-m
    tmux split-window -v -t $SESSION.1
    tmux resize-pane -t $SESSION.1 -y 3
    
    
    
    # Pane 1: Edge server via air
    tmux send-keys -t $SESSION.2 "cd $WORKDIR && air -c $EDGE_AIR_FILE" C-m
    
    # Split Pane 1 horizontally → Pane 2: RTU simulator
    tmux split-window -h -t $SESSION.2
    tmux send-keys -t $SESSION.3 "cd $WORKDIR && air -c $RTU_SIM_AIR_FILE" C-m
   
    
    # Focus back to edge pane
    tmux select-pane -t $SESSION.1
    tmux split-window -h -t $SESSION.1
    tmux select-pane -t $SESSION.2
    
    tmux attach -t $SESSION
}

stop_dev_env() {
    echo "🛑 Stopping development environment..."
    tmux kill-session -t $SESSION 2>/dev/null && echo "✔️  Stopped tmux session '$SESSION'"
    
    echo "🧼 Killing socat..."
    pkill -f "socat -d -d pty,raw,echo=0"
    
    echo "🧼 Stopping Mosquitto container..."
    docker compose --profile dev stop mosquitto
}

case "$1" in
    start )
        start_dev_env false
    ;;
    debug )
        start_dev_env   true
    ;;
    stop )
        stop_dev_env
    ;;
    * )
        print_usage
    ;;
esac
