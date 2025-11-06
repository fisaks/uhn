.PHONY: install-tools

install-tools:
	@echo "📦 Installing system tools..."
	sudo apt-get update
	sudo apt-get install -y mosquitto-clients tmux socat curl git jq

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

	@echo "✅ All tools installed!"
	

dev:
	./devserver.sh start

dev-stop:
	./devserver.sh stop

docker-dev:
	docker compose --profile dev up --build

docker-dev-real:
	docker compose --profile dev-real up --build

docker-build:
	docker compose --profile prod build

