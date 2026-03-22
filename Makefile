BINARY_NAME := teleops
VERSION ?= 1.0.2
BUILD_DIR := build
CMD_PACKAGE := ./cmd
GO ?= go
ZIP ?= zip -j

TARGETS := \
	linux-amd64 \
	linux-armv5 \
	linux-armv6 \
	linux-armv7 \
	windows-amd64

.DEFAULT_GOAL := help

.PHONY: all clean release help $(TARGETS)

all: release

release: clean $(TARGETS)

linux-amd64:
	@$(MAKE) build-target GOOS=linux GOARCH=amd64 OUT_NAME=$(BINARY_NAME)-$(VERSION)-linux-amd64

linux-armv5:
	@$(MAKE) build-target GOOS=linux GOARCH=arm GOARM=5 OUT_NAME=$(BINARY_NAME)-$(VERSION)-linux-armv5

linux-armv6:
	@$(MAKE) build-target GOOS=linux GOARCH=arm GOARM=6 OUT_NAME=$(BINARY_NAME)-$(VERSION)-linux-armv6

linux-armv7:
	@$(MAKE) build-target GOOS=linux GOARCH=arm GOARM=7 OUT_NAME=$(BINARY_NAME)-$(VERSION)-linux-armv7

windows-amd64:
	@$(MAKE) build-target GOOS=windows GOARCH=amd64 OUT_NAME=$(BINARY_NAME)-$(VERSION)-windows-amd64 EXT=.exe

build-target:
	@mkdir -p $(BUILD_DIR)
	@echo "Building $(OUT_NAME) for $(GOOS)/$(GOARCH)$(if $(GOARM), GOARM=$(GOARM),)"
	GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) $(GO) build -ldflags="-s -w" -o $(BUILD_DIR)/$(OUT_NAME)$(EXT) $(CMD_PACKAGE)
	$(ZIP) $(BUILD_DIR)/$(OUT_NAME).zip $(BUILD_DIR)/$(OUT_NAME)$(EXT)
	@rm -f $(BUILD_DIR)/$(OUT_NAME)$(EXT)

clean:
	@echo "Cleaning build directory..."
	@rm -rf $(BUILD_DIR)

help:
	@echo "Available targets:"
	@echo "  make release       Build and package all release archives"
	@echo "  make clean         Remove the build directory"
	@echo "  make $(TARGETS)"
	@echo "Windows-native release helper:"
	@echo "  powershell -ExecutionPolicy Bypass -File .\build-release.ps1"
