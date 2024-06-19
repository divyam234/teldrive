ifdef ComSpec
SHELL := powershell.exe
else
SHELL := /bin/bash
endif

APP_NAME := teldrive
BUILD_DIR := bin
FRONTEND_DIR := ui/dist
FRONTEND_ASSET := https://github.com/divyam234/teldrive-ui/releases/download/v1/teldrive-ui.zip
GIT_TAG := $(shell git describe --tags --abbrev=0)
GIT_COMMIT := $(shell git rev-parse --short HEAD)
GIT_LINK := $(shell git remote get-url origin)
MODULE_PATH := $(shell go list -m)
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
VERSION:= $(GIT_TAG)
BINARY_EXTENSION :=

.PHONY: all build run clean frontend backend run sync-ui retag patch-version minor-version
 
all: build

frontend:
	@echo "Extract UI"
ifeq ($(OS),Windows_NT)
	powershell -Command "Remove-Item -Path $(FRONTEND_DIR) -Recurse -Force"
	powershell -Command "Invoke-WebRequest -Uri $(FRONTEND_ASSET) -OutFile teldrive-ui.zip"
	powershell -Command "if (!(Test-Path -Path $(subst /,\\,$(FRONTEND_DIR)))) { New-Item -ItemType Directory -Force -Path $(subst /,\\,$(FRONTEND_DIR)) }"
	powershell -Command "Expand-Archive -Path teldrive-ui.zip -DestinationPath $(FRONTEND_DIR) -Force"
	powershell -Command "Remove-Item -Path teldrive-ui.zip -Force"
else
	rm -rf $(FRONTEND_DIR)
	curl -LO $(FRONTEND_ASSET) -o teldrive-ui.zip
	mkdir -p $(FRONTEND_DIR)
	unzip -d $(FRONTEND_DIR) teldrive-ui.zip
	rm -rf teldrive-ui.zip
endif

ifeq (${ENV},dev)
    VERSION := dev
endif

ifeq ($(OS),Windows_NT)
    BINARY_EXTENSION := .exe
endif

backend:
	@echo "Building backend for $(GOOS)/$(GOARCH)..."
	go build -trimpath -ldflags "-s -w -X $(MODULE_PATH)/internal/config.Version=$(VERSION) -extldflags=-static" -o $(BUILD_DIR)/$(APP_NAME)$(BINARY_EXTENSION)

build: frontend backend
	@echo "Building complete."

run:
	@echo "Running $(APP_NAME)..."
	$(BUILD_DIR)/$(APP_NAME) run

clean:
	@echo "Cleaning up..."
	rm -rf $(BUILD_DIR)
	cd $(FRONTEND_DIR) && rm -rf dist node_modules

deps:
	@echo "Installing Go dependencies..."
	go mod download

retag:
	@echo "Retagging..."
	git tag -d $(GIT_TAG)
	git push --delete origin $(GIT_TAG)
	git tag -a $(GIT_TAG) -m "Recreated tag $(GIT_TAG)"
	git push origin $(GIT_TAG)

patch-version:
	@echo "Patching version..."
	git tag -a $(shell semver -i patch $(GIT_TAG)) -m "Release $(shell semver -i patch $(GIT_TAG))"
	git push origin $(shell semver -i patch $(GIT_TAG))

minor-version:
	@echo "Minoring version..."
	git tag -a $(shell semver -i minor $(GIT_TAG)) -m "Release $(shell semver -i minor $(GIT_TAG))"
	git push origin $(shell semver -i minor $(GIT_TAG))