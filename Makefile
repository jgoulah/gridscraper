.PHONY: build build-remote install clean install-remote help

BINARY_NAME=gridscraper
INSTALL_PATH=/usr/local/bin
CONFIG_PATH=/usr/local/etc/gridscraper
SYNC_SCRIPT=gridscraper-sync.sh
REMOTE_BIN_PATH=/opt/gridscraper
# your remote install host (only necessary for install-remote)
REMOTE_HOST=mediaserver

help:
	@echo "GridScraper Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build          Build the gridscraper binary"
	@echo "  make install        Install binary and sync script to system"
	@echo "  make install-remote Install binary and sync script to $(REMOTE_HOST)"
	@echo "  make clean          Remove built binary"
	@echo ""
	@echo "Local installation paths:"
	@echo "  Binary:       $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo "  Sync Script:  $(INSTALL_PATH)/$(SYNC_SCRIPT)"
	@echo "  Config:       $(CONFIG_PATH)/"
	@echo ""
	@echo "Remote installation paths ($(REMOTE_HOST)):"
	@echo "  Binary:       $(REMOTE_BIN_PATH)/$(BINARY_NAME)"
	@echo "  Sync Script:  $(REMOTE_BIN_PATH)/$(SYNC_SCRIPT)"
	@echo "  Config:       /usr/local/etc/gridscraper/"

build:
	@echo "Building $(BINARY_NAME)..."
	go build -o $(BINARY_NAME) ./cmd/gridscraper
	@echo "✓ Build complete: $(BINARY_NAME)"

build-remote:
	@echo "Building $(BINARY_NAME) for Linux x86_64..."
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME) ./cmd/gridscraper
	@echo "✓ Build complete: $(BINARY_NAME) (Linux x86_64)"

install: build
	@echo "Installing $(BINARY_NAME) and sync script..."
	sudo install -m 755 $(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)
	sudo install -m 755 scripts/$(SYNC_SCRIPT) $(INSTALL_PATH)/$(SYNC_SCRIPT)
	@echo "✓ Installed $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo "✓ Installed $(INSTALL_PATH)/$(SYNC_SCRIPT)"
	@echo ""
	@echo "Next steps:"
#echo "  1. Copy config.yaml to $(CONFIG_PATH)/config.yaml (if not already there)"
#echo "  2. Copy data.db to $(CONFIG_PATH)/data.db (if not already there)"
#echo "  3. Add to crontab: 0 6 * * * $(INSTALL_PATH)/$(SYNC_SCRIPT) >> $(CONFIG_PATH)/sync.log 2>&1"
#echo "     Or for specific services:"
#echo "     - NYSEG only: $(INSTALL_PATH)/$(SYNC_SCRIPT) nyseg"
#echo "     - ConEd only: $(INSTALL_PATH)/$(SYNC_SCRIPT) coned"

install-remote: build-remote
	@echo "Installing $(BINARY_NAME) and sync script to $(REMOTE_HOST)..."
	scp $(BINARY_NAME) $(REMOTE_HOST):$(REMOTE_BIN_PATH)/$(BINARY_NAME)
	scp scripts/$(SYNC_SCRIPT) $(REMOTE_HOST):$(REMOTE_BIN_PATH)/$(SYNC_SCRIPT)
	ssh $(REMOTE_HOST) "chmod 755 $(REMOTE_BIN_PATH)/$(BINARY_NAME) $(REMOTE_BIN_PATH)/$(SYNC_SCRIPT)"
	@echo "✓ Installed $(REMOTE_HOST):$(REMOTE_BIN_PATH)/$(BINARY_NAME)"
	@echo "✓ Installed $(REMOTE_HOST):$(REMOTE_BIN_PATH)/$(SYNC_SCRIPT)"
# Next steps on the remote host:
#   1. Create config.yaml in /usr/local/etc/gridscraper/ (if not already there)
#   2. Create data.db in /usr/local/etc/gridscraper/ (if not already there)
#   3. Add to crontab: 0 6 * * * /opt/gridscraper/gridscraper-sync.sh >> /usr/local/etc/gridscraper/sync.log 2>&1
#      Or for specific services:
#      - NYSEG only: /opt/gridscraper/gridscraper-sync.sh nyseg
#      - ConEd only: /opt/gridscraper/gridscraper-sync.sh coned

clean:
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	@echo "✓ Clean complete"
