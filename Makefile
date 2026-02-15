PLUGIN_ID = com.scientia.voice-message
PLUGIN_VERSION = 2.0.0
BUNDLE = $(PLUGIN_ID)-$(PLUGIN_VERSION).tar.gz
GO ?= go
GOFLAGS = -trimpath
PLATFORMS = linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64

.PHONY: all server webapp dist dev deploy clean

all: dist

server:
	@echo "==> Building server..."
	@mkdir -p server/dist
	@for p in $(PLATFORMS); do \
		os=$$(echo $$p | cut -d- -f1); \
		arch=$$(echo $$p | cut -d- -f2); \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "    $$p"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -o server/dist/plugin-$$p$$ext ./server; \
	done

webapp:
	@echo "==> Building webapp..."
	@cd webapp && npm install --legacy-peer-deps 2>/dev/null && npm run build

dist: server webapp
	@echo "==> Packaging..."
	@rm -rf dist/$(PLUGIN_ID)
	@mkdir -p dist/$(PLUGIN_ID)/server/dist dist/$(PLUGIN_ID)/webapp/dist dist/$(PLUGIN_ID)/assets
	@cp plugin.json dist/$(PLUGIN_ID)/
	@cp assets/icon.svg dist/$(PLUGIN_ID)/assets/
	@cp server/dist/* dist/$(PLUGIN_ID)/server/dist/
	@cp webapp/dist/main.js dist/$(PLUGIN_ID)/webapp/dist/
	@cd dist && tar -czvf $(BUNDLE) $(PLUGIN_ID)
	@echo "\n✅  dist/$(BUNDLE)"

server-dev:
	@mkdir -p server/dist
	$(GO) build $(GOFLAGS) -o server/dist/plugin-$$(go env GOOS)-$$(go env GOARCH) ./server

dev: server-dev webapp

deploy: dist
	@[ -n "$(MM_SERVICESETTINGS_SITEURL)" ] || (echo "Set MM_SERVICESETTINGS_SITEURL"; exit 1)
	curl -f -s -H "Authorization: Bearer $(MM_ADMIN_TOKEN)" \
		-F "plugin=@dist/$(BUNDLE)" -F "force=true" \
		$(MM_SERVICESETTINGS_SITEURL)/api/v4/plugins
	@echo "\n✅  Deployed. Enable in System Console."

clean:
	rm -rf server/dist webapp/dist webapp/node_modules dist
