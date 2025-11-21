.PHONY: install-tools

install-tools:
	@echo "📦 Installing system tools..."
	sudo apt-get update
	sudo apt-get install -y mosquitto-clients tmux socat curl git jq python3.10-venv python3-pip python3

	@echo "🐹 Installing Go (if missing)..."
	@if ! command -v go >/dev/null 2>&1; then \
		curl -OL https://golang.org/dl/go1.21.6.linux-amd64.tar.gz && \
		sudo rm -rf /usr/local/go && \
		sudo tar -C /usr/local -xzf go1.21.6.linux-amd64.tar.gz && \
		echo "export PATH=\$$PATH:/usr/local/go/bin" >> $$HOME/.bashrc; \
		echo "✅ Go installed — restart your shell"; \
	else \
		echo "✅ Go is already installed."; \
	fi

	@echo "🔧 Installing Go tools from go.mod..."
	go install github.com/air-verse/air@$(shell go list -m -f '{{.Version}}' github.com/air-verse/air)
	go install github.com/go-delve/delve/cmd/dlv@$(shell go list -m -f '{{.Version}}' github.com/go-delve/delve)
	
	@echo "✅ All tools installed!"	
																

dev:
	./devserver.sh start

dev-stop:
	./devserver.sh stop

docker-dev:
	docker compose --profile dev up --build

docker-test:
	docker compose --profile test up --build


docker-test-stop:
	docker compose -profile test down

docker-dev-real:
	docker compose --profile dev-real up --build

docker-build:
	docker compose --profile prod build

docker-stop:
	docker compose down
build-uhn-tools:
	go build -o bin/uhnctl ./cmd/tools/uhnctl
	go build -o bin/uhn-monitor ./cmd/tools/monitor/uhn-monitor.go

