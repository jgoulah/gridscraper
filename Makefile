.PHONY: build install clean help

BINARY_NAME=gridscraper
INSTALL_PATH=/usr/local/bin
CONFIG_PATH=/usr/local/etc/gridscraper
NYSEG_SCRIPT=gridscraper-sync.sh
CONED_SCRIPT=gridscraper-sync-coned.sh

help:
	@echo "GridScraper Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build        Build the gridscraper binary"
	@echo "  make install      Install binary and sync scripts to system"
	@echo "  make clean        Remove built binary"
	@echo ""
	@echo "Installation paths:"
	@echo "  Binary:       $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo "  NYSEG Script: $(INSTALL_PATH)/$(NYSEG_SCRIPT)"
	@echo "  ConEd Script: $(INSTALL_PATH)/$(CONED_SCRIPT)"
	@echo "  Config:       $(CONFIG_PATH)/"

build:
	@echo "Building $(BINARY_NAME)..."
	go build -o $(BINARY_NAME) ./cmd/gridscraper
	@echo "✓ Build complete: $(BINARY_NAME)"

install: build
	@echo "Installing $(BINARY_NAME) and sync scripts..."
	sudo install -m 755 $(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)
	sudo install -m 755 scripts/$(NYSEG_SCRIPT) $(INSTALL_PATH)/$(NYSEG_SCRIPT)
	sudo install -m 755 scripts/$(CONED_SCRIPT) $(INSTALL_PATH)/$(CONED_SCRIPT)
	@echo "✓ Installed $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo "✓ Installed $(INSTALL_PATH)/$(NYSEG_SCRIPT)"
	@echo "✓ Installed $(INSTALL_PATH)/$(CONED_SCRIPT)"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Copy config.yaml to $(CONFIG_PATH)/config.yaml (if not already there)"
	@echo "  2. Copy data.db to $(CONFIG_PATH)/data.db (if not already there)"
	@echo "  3. Add to crontab for NYSEG:  0 6 * * * $(INSTALL_PATH)/$(NYSEG_SCRIPT) >> $(CONFIG_PATH)/nyseg.log 2>&1"
	@echo "  4. Add to crontab for ConEd:  0 6 * * * $(INSTALL_PATH)/$(CONED_SCRIPT) >> $(CONFIG_PATH)/coned.log 2>&1"

clean:
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	@echo "✓ Clean complete"
