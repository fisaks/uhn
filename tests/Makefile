# Makefile (put in your project root)

VENV := .venv
PY := $(VENV)/bin/python
PIP := $(PY) -m pip
PIP_COMPILE := $(VENV)/bin/pip-compile
PIP_SYNC := $(VENV)/bin/pip-sync

.PHONY: create install install-dev compile add upgrade sync clean

create:
	python3 -m venv $(VENV)
	$(PIP) install --upgrade pip setuptools wheel black

install: create
	$(PIP) install -r requirements.txt

install-dev: create
	$(PIP) install -r requirements-dev.txt

# Compile requirements.in -> requirements.txt (installs pip-tools if needed)
compile: create
	$(PIP) install --upgrade pip-tools
	$(PIP_COMPILE) requirements.in --output-file requirements.txt --generate-hashes
	@if [ -f requirements-dev.in ]; then \
	  $(PIP_COMPILE) requirements-dev.in --output-file requirements-dev.txt --generate-hashes; \
	fi

# Add a top-level package and recompile lockfile
# Usage: make add PKG="requests>=2.32"
add: create
	@if [ -z "$(PKG)" ]; then echo "Usage: make add PKG=\"package[extras]>=x.y\""; exit 1; fi
	# append the package to requirements.in (avoid duplicates by manual edit if you prefer)
	printf "%s\n" "$(PKG)" >> requirements.in
	make compile

# Upgrade a single package in the lockfile (safe way to update only one)
# Usage: make upgrade PKG=requests
upgrade: create
	@if [ -z "$(PKG)" ]; then echo "Usage: make upgrade PKG=<name>"; exit 1; fi
	$(PIP) install --upgrade pip-tools
	$(PIP_COMPILE) --upgrade-package $(PKG) requirements.in --output-file requirements.txt --generate-hashes

# Sync the virtualenv to exact pinned requirements (requires pip-tools' pip-sync)
sync: create
	$(PIP) install --upgrade pip-tools
	$(PIP_SYNC) requirements.txt

# Remove venv (useful to start clean)
clean:
	rm -rf $(VENV)
