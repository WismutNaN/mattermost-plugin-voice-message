PLUGIN_ID := com.scientia.voice-message
PLUGIN_VERSION := 2.0.1
BUNDLE := $(PLUGIN_ID)-$(PLUGIN_VERSION).tar.gz
GO ?= go
GOFLAGS ?= -trimpath
PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64

.PHONY: all server webapp dist server-dev dev deploy clean

all: dist

# -------------------------
# OS-specific helpers
# -------------------------
ifeq ($(OS),Windows_NT)
  DEVNULL := NUL
  PS := powershell.exe -NoProfile -ExecutionPolicy Bypass -Command

  define RM_RF
	$(PS) "if (Test-Path -LiteralPath '$(1)') { Remove-Item -LiteralPath '$(1)' -Recurse -Force }"
  endef
  define MKDIR_P
	$(PS) "New-Item -ItemType Directory -Force '$(1)' | Out-Null"
  endef
  define CP_FILE
	$(PS) "if (-not (Test-Path -LiteralPath '$(1)')) { Write-Error 'Missing file: $(1)'; exit 1 }; Copy-Item -LiteralPath '$(1)' -Destination '$(2)' -Force"
  endef
  define CP_GLOB
	$(PS) "Copy-Item -Path \"$(1)\" -Destination \"$(2)\" -Force"
  endef
  define GO_BUILD
	$(PS) "Set-Item -Path Env:CGO_ENABLED -Value '0'; Set-Item -Path Env:GOOS -Value '$(1)'; Set-Item -Path Env:GOARCH -Value '$(2)'; & '$(GO)' build $(GOFLAGS) -o '$(3)' ./server"
  endef
  # Use pack.go to create tar.gz with correct Unix permissions (0755 on binaries).
  # Windows tar.exe does NOT preserve Unix execute bits, which causes "permission denied" on Linux.
  define TAR_GZ
	$(GO) run pack.go
  endef
else
  DEVNULL := /dev/null
  define RM_RF
	rm -rf $(1)
  endef
  define MKDIR_P
	mkdir -p $(1)
  endef
  define CP_FILE
	test -f $(1)
	cp -f $(1) $(2)
  endef
  define CP_GLOB
	cp -f $(1) $(2)
  endef
  define GO_BUILD
	CGO_ENABLED=0 GOOS=$(1) GOARCH=$(2) $(GO) build $(GOFLAGS) -o $(3) ./server
  endef
  define TAR_GZ
	cd dist && tar -czvf $(BUNDLE) $(PLUGIN_ID)
  endef
endif

# -------------------------
# Small make "parsers"
# -------------------------
goos   = $(word 1,$(subst -, ,$(1)))
goarch = $(word 2,$(subst -, ,$(1)))
goext  = $(if $(filter windows,$(call goos,$(1))),.exe,)

SERVER_OUTPUTS := $(foreach p,$(PLATFORMS),server/dist/plugin-$(p)$(call goext,$(p)))

# -------------------------
# Targets
# -------------------------
server: $(SERVER_OUTPUTS)
	@echo "==> Server binaries ready."

define SERVER_RULE
server/dist/plugin-$(1)$(call goext,$(1)):
	@echo "==> Building server for $(1)..."
	@$(call MKDIR_P,server/dist)
	@$(call GO_BUILD,$(call goos,$(1)),$(call goarch,$(1)),$$@)
endef
$(foreach p,$(PLATFORMS),$(eval $(call SERVER_RULE,$(p))))

webapp:
	@echo "==> Building webapp..."
	@cd webapp && npm install --legacy-peer-deps 2>$(DEVNULL) && npm run build

dist: server webapp
	@echo "==> Packaging..."
	@$(call RM_RF,dist/$(PLUGIN_ID))
	@$(call MKDIR_P,dist/$(PLUGIN_ID)/server/dist)
	@$(call MKDIR_P,dist/$(PLUGIN_ID)/webapp/dist)
	@$(call MKDIR_P,dist/$(PLUGIN_ID)/assets)
	@$(call CP_FILE,plugin.json,dist/$(PLUGIN_ID)/)
	@$(call CP_FILE,assets/icon.svg,dist/$(PLUGIN_ID)/assets/)
	@$(call CP_GLOB,server/dist/*,dist/$(PLUGIN_ID)/server/dist/)
	@$(call CP_FILE,webapp/dist/main.js,dist/$(PLUGIN_ID)/webapp/dist/)
	@$(call TAR_GZ)

# Host-only build (dev)
HOST_OS := $(shell $(GO) env GOOS)
HOST_ARCH := $(shell $(GO) env GOARCH)
HOST_EXT := $(if $(filter windows,$(HOST_OS)),.exe,)
SERVER_DEV_OUT := server/dist/plugin-dev-$(HOST_OS)-$(HOST_ARCH)$(HOST_EXT)

server-dev: $(SERVER_DEV_OUT)
	@echo "==> Dev server binary ready: $(SERVER_DEV_OUT)"

$(SERVER_DEV_OUT):
	@echo "==> Building server-dev for $(HOST_OS)-$(HOST_ARCH)..."
	@$(call MKDIR_P,server/dist)
	@$(call GO_BUILD,$(HOST_OS),$(HOST_ARCH),$@)

dev: server-dev webapp

deploy: dist
	@$(if $(strip $(MM_SERVICESETTINGS_SITEURL)),,$(error MM_SERVICESETTINGS_SITEURL is not set))
	@$(if $(strip $(MM_ADMIN_TOKEN)),,$(error MM_ADMIN_TOKEN is not set))
	curl -f -s -H "Authorization: Bearer $(MM_ADMIN_TOKEN)" \
		-F "plugin=@dist/$(BUNDLE)" -F "force=true" \
		$(MM_SERVICESETTINGS_SITEURL)/api/v4/plugins
	@echo
	@echo "✅  Deployed. Enable in System Console."

clean:
	@echo "==> Cleaning..."
	@$(call RM_RF,server/dist)
	@$(call RM_RF,webapp/dist)
	@$(call RM_RF,webapp/node_modules)
	@$(call RM_RF,dist)
