#!/bin/bash

WORKDIR=$(pwd)

print_usage() {
    echo "Usage: $0 [profile] {start|stop|debug}"
    echo ""
    echo "  $0 start             dev profile (default)"
    echo "  $0 live start        live profile"
    echo "  $0 live debug        live profile with dlv"
    echo "  $0 live stop         stop live session"
    echo ""
    echo "Profiles:"
    echo "  dev    Simulators: socat serial ports, Modbus sim, IHC sim"
    echo "  live   Real hardware: physical serial ports, real IHC controller"
    echo ""
    echo "Config files per profile:"
    echo "  config/edge-config-{profile}.json    Edge configuration"
    echo "  config/devserver-{profile}.conf       Simulator flags"
}

start_dev_env() {
    local debug="${1:-false}"
    local profile="${2:-dev}"

    SESSION="uhn-${profile}"
    local CONFIG_FILE="config/edge-config-${profile}.json"
    local ENV_FILE="config/devserver-${profile}.conf"

    # Validate config + env files exist
    if [[ ! -f "$CONFIG_FILE" ]]; then
        echo "ERROR: Config file not found: $CONFIG_FILE"
        exit 1
    fi
    if [[ ! -f "$ENV_FILE" ]]; then
        echo "ERROR: Env file not found: $ENV_FILE"
        exit 1
    fi

    # Source env file for simulator flags
    source "$ENV_FILE"

    # Resolve timezone: env TZ > /etc/timezone > UTC
    if [[ -z "$TZ" ]]; then
        if [[ -f /etc/timezone ]]; then
            export TZ=$(cat /etc/timezone)
        else
            export TZ=UTC
        fi
    fi

    # Detect Zigbee USB dongle path (stable /dev/serial/by-id/ path)
    ZIGBEE_DEV=$(ls /dev/serial/by-id/*Sonoff*Zigbee* /dev/serial/by-id/*Silicon_Labs* 2>/dev/null | head -1)
    if [[ -n "$ZIGBEE_DEV" ]]; then
        export ZIGBEE_DEVICE="$ZIGBEE_DEV"
    fi

    # Auto-detect ZIGBEE_Z2M if not explicitly set in conf
    if [[ -z "$ZIGBEE_Z2M" ]]; then
        if [[ -n "$ZIGBEE_DEV" ]]; then
            ZIGBEE_Z2M=true
        else
            ZIGBEE_Z2M=false
        fi
    fi

    echo "Starting development environment: profile=$profile, session=$SESSION"
    echo "  Config: $CONFIG_FILE"
    echo "  Env:    $ENV_FILE"
    echo "  MODBUS_SIM=$MODBUS_SIM, IHC_SIM=$IHC_SIM, MILIGHT_SIM=$MILIGHT_SIM, ZIGBEE_Z2M=$ZIGBEE_Z2M"
    if [[ -n "$ZIGBEE_DEV" ]]; then
        echo "  Zigbee dongle: $ZIGBEE_DEV"
    fi

    if [[ "$debug" == "true" ]]; then
        echo "  Mode: debug hot reload"
        EDGE_AIR_FILE=".air-dvl.toml"
        SIM_AIR_FILE=".air-sim-dvl.toml"
    else
        echo "  Mode: hot reload"
        EDGE_AIR_FILE=".air.toml"
        SIM_AIR_FILE=".air-sim.toml"
    fi

    # Start Mosquitto container before anything else
    if ! docker ps --format '{{.Names}}' | grep -q '^uhn-mosquitto$'; then
        echo "Starting Mosquitto via Docker Compose..."
        docker compose --profile dev up -d mosquitto
    else
        echo "Mosquitto already running"
    fi

    # Start Zigbee2MQTT container if enabled (skip if sim takes over)
    if [[ "$ZIGBEE_Z2M" == "true" && "$ZIGBEE_SIM" != "true" ]]; then
        if ! docker ps --format '{{.Names}}' | grep -q '^uhn-zigbee2mqtt$'; then
            echo "Starting Zigbee2MQTT via Docker Compose..."
            docker compose --profile zigbee up -d zigbee2mqtt
        else
            echo "Zigbee2MQTT already running"
        fi
    fi

    echo "Waiting for Mosquitto to be ready on localhost:1883..."
    for i in {1..10}; do
        if nc -z localhost 1883; then
            echo "Mosquitto is up!"
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

    # NOT exported — these are passed explicitly to the tmux session via -e flags.
    # Exporting would pollute the tmux server's global environment and leak into
    # other tmux sessions (e.g. the UXP devserver).
    UHN_EDGE_CONFIG_PATH="$WORKDIR/tmp/edge-config-${profile}.json"
    SIM_CONFIG_PATH="$WORKDIR/tmp/sim-config-${profile}.json"
    UHN_WORKSPACE_PATH="$WORKDIR/tmp/uhn-workspace"
    UHN_NODE_PATH="/home/freddi/.nvm/versions/node/v22.11.0"
    UHN_RUNTIME_PATH="/home/freddi/Projects/uxp"
    TZ="Europe/Helsinki"

    mkdir -p "$UHN_WORKSPACE_PATH"
    rm -f "$UHN_EDGE_CONFIG_PATH" "$SIM_CONFIG_PATH"

    # Patch config: socat serial ports only when MODBUS_SIM is enabled
    if [[ "$MODBUS_SIM" == "true" ]]; then
        echo "Creating virtual serial ports with socat..."
        SOCAT_LOG=$(mktemp)
        socat -d -d pty,raw,echo=0 pty,raw,echo=0 2>"$SOCAT_LOG" &
        SOCAT_PID=$!
        sleep 1
        PORTS=$(grep -o '/dev/pts/[0-9]\+' "$SOCAT_LOG")
        EDGE_PORT=$(echo "$PORTS" | sed -n 1p)
        SIM_PORT=$(echo "$PORTS" | sed -n 2p)

        jq --arg port "$EDGE_PORT" '.buses[0].port = $port' "$CONFIG_FILE" > "$UHN_EDGE_CONFIG_PATH"
        jq --arg port "$SIM_PORT" '.buses[0].port = $port' "$CONFIG_FILE" > "$SIM_CONFIG_PATH"

        echo "  Edge serial: $EDGE_PORT"
        echo "  Sim serial:  $SIM_PORT"
        echo "EDGE_PORT=$EDGE_PORT" > tmp/ports.env
        echo "SIM_PORT=$SIM_PORT" >> tmp/ports.env
    else
        # No socat — use config as-is
        cp "$CONFIG_FILE" "$UHN_EDGE_CONFIG_PATH"
        EDGE_PORT=""
        SIM_PORT=""
    fi

    # Patch IHC credentials path to absolute when IHC_SIM is enabled
    if [[ "$IHC_SIM" == "true" ]]; then
        local creds_rel
        creds_rel=$(jq -r '.ihcCredentialsFile // empty' "$UHN_EDGE_CONFIG_PATH")
        if [[ -n "$creds_rel" && ! "$creds_rel" = /* ]]; then
            jq --arg path "$WORKDIR/$creds_rel" '.ihcCredentialsFile = $path' "$UHN_EDGE_CONFIG_PATH" > "${UHN_EDGE_CONFIG_PATH}.tmp"
            mv "${UHN_EDGE_CONFIG_PATH}.tmp" "$UHN_EDGE_CONFIG_PATH"
        fi
    fi

    UHN_MQTT_URL=tcp://localhost:1883
    UHN_EDGE_NAME=edge1
    UHN_LOG_LEVEL=debug
    UHN_PUBLIC_HOST=$(hostname -I | awk '{print $1}')

    echo "  Log level: $UHN_LOG_LEVEL"
    echo "  MQTT: $UHN_MQTT_URL"
    echo "  Config: $UHN_EDGE_CONFIG_PATH"

    # --- Window 1: dev (monitor, mosquitto, edge) ---

    SIM_BASE_CONFIG="$WORKDIR/$CONFIG_FILE"

    tmux new-session -d -s $SESSION -n dev \
        -e EDGE_PORT="$EDGE_PORT" \
        -e SIM_PORT="$SIM_PORT" \
        -e SIM_BASE_CONFIG="$SIM_BASE_CONFIG" \
        -e UHN_EDGE_CONFIG_PATH="$UHN_EDGE_CONFIG_PATH" \
        -e SIM_CONFIG_PATH="$SIM_CONFIG_PATH" \
        -e UHN_WORKSPACE_PATH="$UHN_WORKSPACE_PATH" \
        -e "UHN_NODE_PATH=$UHN_NODE_PATH" \
        -e "UHN_RUNTIME_PATH=$UHN_RUNTIME_PATH" \
        -e "TZ=$TZ" \
        -e UHN_MQTT_URL="$UHN_MQTT_URL" \
        -e UHN_EDGE_NAME="$UHN_EDGE_NAME" \
        -e UHN_LOG_LEVEL="$UHN_LOG_LEVEL" \
        -e UHN_PUBLIC_HOST="$UHN_PUBLIC_HOST"

    # Pane 0: MQTT monitor
    tmux send-keys -t $SESSION.0 "go build -o tmp/uhn-monitor ./cmd/tools/monitor && ./tmp/uhn-monitor" C-m

    # Pane 1: Mosquitto logs
    tmux split-window -v -t $SESSION.0
    tmux resize-pane -t $SESSION.0 -y 15
    tmux send-keys -t $SESSION.1 \
        "echo 'Showing Mosquitto logs (press Ctrl-b d to detach)' && docker logs -f uhn-mosquitto" C-m

    # Pane 2: Edge server
    tmux split-window -v -t $SESSION.1
    tmux resize-pane -t $SESSION.1 -y 15
    tmux send-keys -t $SESSION.2 "cd $WORKDIR && air -c $EDGE_AIR_FILE" C-m

    # Pane 3: empty shell
    tmux select-pane -t $SESSION.1
    tmux split-window -h -t $SESSION.1
    tmux select-pane -t $SESSION.2

    # --- Window 2: sims (2x2 grid: Modbus/IHC left, Mi-Light/Zigbee right) ---
    #
    # Layout:  0 Modbus    | 2 Mi-Light
    #          1 IHC       | 3 Zigbee

    tmux new-window -t $SESSION -n sims

    # Split into left/right columns
    tmux split-window -h -t $SESSION:sims.0

    # Split left column (pane 0) into top/bottom
    tmux split-window -v -t $SESSION:sims.0

    # Split right column (pane 2) into top/bottom
    tmux split-window -v -t $SESSION:sims.2

    # Pane 0: Modbus (top-left)
    if [[ "$MODBUS_SIM" == "true" ]]; then
        tmux send-keys -t $SESSION:sims.0 "cd $WORKDIR && air -c $SIM_AIR_FILE" C-m
    else
        tmux send-keys -t $SESSION:sims.0 "echo 'Modbus sim disabled for profile: ${profile}'" C-m
    fi

    # Pane 1: IHC (bottom-left)
    if [[ "$IHC_SIM" == "true" ]]; then
        tmux send-keys -t $SESSION:sims.1 "cd $WORKDIR && air -c .air-ihc-sim.toml" C-m
    else
        tmux send-keys -t $SESSION:sims.1 "echo 'IHC sim disabled for profile: ${profile}'" C-m
    fi

    # Pane 2: Mi-Light (top-right)
    if [[ "$MILIGHT_SIM" == "true" ]]; then
        tmux send-keys -t $SESSION:sims.2 "cd $WORKDIR && air -c .air-milight-sim.toml" C-m
    else
        tmux send-keys -t $SESSION:sims.2 "echo 'Mi-Light sim disabled for profile: ${profile}'" C-m
    fi

    # Pane 3: Zigbee (bottom-right) — sim takes priority over Z2M
    if [[ "$ZIGBEE_SIM" == "true" ]]; then
        tmux send-keys -t $SESSION:sims.3 "cd $WORKDIR && air -c .air-zigbee-sim.toml" C-m
    elif [[ "$ZIGBEE_Z2M" == "true" ]]; then
        tmux send-keys -t $SESSION:sims.3 "docker logs -f uhn-zigbee2mqtt" C-m
    else
        tmux send-keys -t $SESSION:sims.3 "echo 'Zigbee: no USB dongle detected and ZIGBEE_Z2M/ZIGBEE_SIM not set'" C-m
    fi

    # Focus back to dev window
    tmux select-window -t $SESSION:dev

    tmux attach -t $SESSION
}

stop_dev_env() {
    local profile="${1:-dev}"
    SESSION="uhn-${profile}"

    echo "Stopping development environment: profile=$profile, session=$SESSION"
    tmux kill-session -t $SESSION 2>/dev/null && echo "Stopped tmux session '$SESSION'"

    echo "Killing socat..."
    pkill -f "socat -d -d pty,raw,echo=0"

    echo "Stopping Zigbee2MQTT container..."
    docker compose --profile zigbee stop zigbee2mqtt 2>/dev/null

    echo "Stopping Mosquitto container..."
    docker compose --profile dev stop mosquitto
}

# Parse args: [profile] command
# If first arg is a known command, profile defaults to "dev"
# Otherwise first arg is the profile and second is the command
case "$1" in
    start|stop|debug )
        PROFILE="dev"
        COMMAND="$1"
    ;;
    "" )
        print_usage
        exit 0
    ;;
    * )
        PROFILE="$1"
        COMMAND="$2"
    ;;
esac

case "$COMMAND" in
    start )
        start_dev_env false "$PROFILE"
    ;;
    debug )
        start_dev_env true "$PROFILE"
    ;;
    stop )
        stop_dev_env "$PROFILE"
    ;;
    * )
        print_usage
    ;;
esac
