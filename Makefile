.PHONY: build install clean help

BINARY_NAME=gridscraper
INSTALL_PATH=/usr/local/bin
CONFIG_PATH=/usr/local/etc/gridscraper
SCRIPT_NAME=gridscraper-sync.sh

help:
	@echo "GridScraper Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build        Build the gridscraper binary"
	@echo "  make install      Install binary and sync script to system"
	@echo "  make clean        Remove built binary"
	@echo ""
	@echo "Installation paths:"
	@echo "  Binary:  $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo "  Script:  $(INSTALL_PATH)/$(SCRIPT_NAME)"
	@echo "  Config:  $(CONFIG_PATH)/"

build:
	@echo "Building $(BINARY_NAME)..."
	go build -o $(BINARY_NAME) ./cmd/gridscraper
	@echo "✓ Build complete: $(BINARY_NAME)"

install: build
	@echo "Installing $(BINARY_NAME) and $(SCRIPT_NAME)..."
	sudo install -m 755 $(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)
	sudo install -m 755 scripts/$(SCRIPT_NAME) $(INSTALL_PATH)/$(SCRIPT_NAME)
	@echo "✓ Installed $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo "✓ Installed $(INSTALL_PATH)/$(SCRIPT_NAME)"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Copy config.yaml to $(CONFIG_PATH)/config.yaml (if not already there)"
	@echo "  2. Copy data.db to $(CONFIG_PATH)/data.db (if not already there)"
	@echo "  3. Add to crontab: 0 6 * * * $(INSTALL_PATH)/$(SCRIPT_NAME) >> $(CONFIG_PATH)/gridscraper.log 2>&1"

clean:
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	@echo "✓ Clean complete"
