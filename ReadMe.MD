# Unified Home Network

## Dev setup

Install tools:

```sh
go install github.com/air-verse/air@$(go list -m -f '{{.Version}}' github.com/air-verse/air)
```

or just `make install-tools`

## Development environment

Starts the development server in a tmux session with multiple windows for different services

`make dev`

Press `Ctrl-b + d` to detach the session, then run the following command to stop the development server:

`make dev-stop`

Can mouse copy when holding shift down

Uses go Air for go watch devserver.sh start mqtt in docker uhn runs locally

## Docker

To run dev in docker using rtu simulation software `docker compose --profile dev up --build` or `make docker-dev`

To run dev-real in docker using real rtu devices `docker compose --profile dev-real up --build` or `make docker-dev`-real

To build prod images `docker compose --profile prod build` or `make docker-build`

## MQTT

To tail messages

`docker exec -it uhn-mosquitto sh -c 'apk add --no-cache mosquitto-clients >/dev/null 2>&1 || true; mosquitto_sub -h localhost -t "uhn/#" -v'`

## Tools

To see the base64 binary data as bits `echo AQ== | base64 -d | od -An -t u1`

## Navigating the tmux Environment

- **Detach the tmux Session:** Press `Ctrl+b` and then `d` to detach the session while leaving the processes running.
- Can mouse copy when holding shift down
- **Switch Between Panes:** Use `Ctrl+b` followed by an `arrow key` to switch between panes.
- **Toggle Pane Size:** To make the current pane the whole window, press `Ctrl+b` and then `z`. To toggle back, press `Ctrl+b` and then `z` again.
- **Reattach to the tmux Session:** Run `tmux attach-session -t uhn-dev` or just `devserver.sh start`.
- **Kill the tmux Session:** Exit all panes or kill the session with `tmux kill-session -t uhn-dev` or just `devserver.sh stop`.

`tmux kill-session -t uhn-dev` or just `devserver.sh stop`

`tmux kill-server` Will kill the whole tmux server
