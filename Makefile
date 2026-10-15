# Go environment
GO      ?= $(shell command -v go)
GOARCH  ?= $(shell go env GOARCH)
GOOS    ?= $(shell go env GOOS)

APPLICATION := bellek
DIST_DIR    := dist
BINARY      := $(DIST_DIR)/$(APPLICATION)-$(GOOS)-$(GOARCH)

# Version from git tags
VERSION ?= $(shell git describe --tags 2>/dev/null)
VERSION := $(if $(strip $(VERSION)),$(VERSION),dev)

RELEASE_PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64
SHA256SUM := $(or $(shell command -v sha256sum),shasum -a 256)

BUILD_FLAGS := -trimpath -ldflags '-s -w -X main.Version=$(VERSION)'

# Claude Code home the plugin targets install into, one home at a time
CLAUDE_CONFIG_DIR ?= $(HOME)/.claude

# sudo is used for the copy alone and only when INSTALL_DIR is not writable, so a build
# never writes anything as root.
INSTALL_DIR ?= /usr/local/bin
SUDO := $(shell test -w $(INSTALL_DIR) || echo sudo)

TAP_REPOSITORY ?= bellek-si/homebrew-tap
FORMULA        := $(DIST_DIR)/$(APPLICATION).rb

.PHONY: help
help:
	@echo ""
	@echo "\033[1mUsage:\033[0m"
	@echo "  make <target>"
	@echo ""
	@echo "\033[1mDevelopment:\033[0m"
	@grep -E '^(dep|test|lint|clean|ci):.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "\033[1mBuild and install:\033[0m"
	@grep -E '^(build|install|uninstall|plugin-.*):.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "\033[1mRelease:\033[0m"
	@grep -E '^(package|release-assets|github-release|release-upload|homebrew-.*|release):.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "\033[1mVariables:\033[0m"
	@echo "  \033[36mVERSION\033[0m             $(VERSION)"
	@echo "  \033[36mGOOS\033[0m                $(GOOS)"
	@echo "  \033[36mGOARCH\033[0m              $(GOARCH)"
	@echo "  \033[36mINSTALL_DIR\033[0m         $(INSTALL_DIR)"
	@echo "  \033[36mCLAUDE_CONFIG_DIR\033[0m   $(CLAUDE_CONFIG_DIR)"
	@echo ""

# =============================================================================
# Development
# =============================================================================

.PHONY: dep
dep: ## Tidy and verify the Go modules
	@$(GO) mod tidy
	@$(GO) mod verify

.PHONY: test
test: ## Run the tests with the race detector
	@$(GO) test -race -cover ./...

.PHONY: lint
lint: ## Check formatting, then run golangci-lint when installed, else go vet
	@test -z "$$(gofmt -l .)" || { echo "gofmt would change:"; gofmt -l .; exit 1; }
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else $(GO) vet ./...; fi

.PHONY: clean
clean: ## Remove the build output
	@rm -rf $(DIST_DIR)

.PHONY: ci
ci: lint test ## Run every check the ci workflow runs

# =============================================================================
# Build and install
# =============================================================================

.PHONY: build
build: ## Build bellek for the current platform into dist
	@mkdir -p $(DIST_DIR)
	@CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(BUILD_FLAGS) -o $(BINARY) ./cmd/$(APPLICATION)
	@ln -sf $(notdir $(BINARY)) $(DIST_DIR)/$(APPLICATION)
	@echo "Built $(BINARY)"

.PHONY: install
install: build ## Build as you, then copy bellek into INSTALL_DIR, with sudo only when needed
	@$(SUDO) rm -f $(INSTALL_DIR)/$(APPLICATION)
	@$(SUDO) cp $(BINARY) $(INSTALL_DIR)/$(APPLICATION)
	@$(SUDO) chmod +x $(INSTALL_DIR)/$(APPLICATION)
	@echo "Installed $(INSTALL_DIR)/$(APPLICATION)"

.PHONY: uninstall
uninstall: ## Remove bellek from INSTALL_DIR
	@$(SUDO) rm -f $(INSTALL_DIR)/$(APPLICATION)

.PHONY: plugin-install
plugin-install: ## Register this checkout as a plugin marketplace and install the plugin into CLAUDE_CONFIG_DIR
	@CLAUDE_CONFIG_DIR=$(CLAUDE_CONFIG_DIR) claude plugin marketplace add $(CURDIR)
	@CLAUDE_CONFIG_DIR=$(CLAUDE_CONFIG_DIR) claude plugin install $(APPLICATION)@$(APPLICATION) --scope user

.PHONY: plugin-update
plugin-update: ## Refresh the installed plugin from this checkout, plugin files are copied on install
	@CLAUDE_CONFIG_DIR=$(CLAUDE_CONFIG_DIR) claude plugin marketplace update $(APPLICATION)
	@CLAUDE_CONFIG_DIR=$(CLAUDE_CONFIG_DIR) claude plugin uninstall $(APPLICATION)@$(APPLICATION)
	@CLAUDE_CONFIG_DIR=$(CLAUDE_CONFIG_DIR) claude plugin install $(APPLICATION)@$(APPLICATION) --scope user

.PHONY: plugin-uninstall
plugin-uninstall: ## Remove the plugin and the marketplace from CLAUDE_CONFIG_DIR
	@CLAUDE_CONFIG_DIR=$(CLAUDE_CONFIG_DIR) claude plugin uninstall $(APPLICATION)@$(APPLICATION)
	@CLAUDE_CONFIG_DIR=$(CLAUDE_CONFIG_DIR) claude plugin marketplace remove $(APPLICATION)

# =============================================================================
# Release
# =============================================================================

.PHONY: package
package: build ## Pack bellek, README.md and LICENSE as dist/bellek-<os>-<arch>.tar.gz
	@tar -czhf $(DIST_DIR)/$(APPLICATION)-$(GOOS)-$(GOARCH).tar.gz -C $(DIST_DIR) $(APPLICATION) -C $(CURDIR) README.md LICENSE

# The asset names carry no version, so releases/latest/download/<name> always
# points at the newest one.
.PHONY: release-assets
release-assets: ## Pack the four release platforms and write checksums.txt
	@rm -f $(DIST_DIR)/*.tar.gz $(DIST_DIR)/checksums.txt
	@for p in $(RELEASE_PLATFORMS); do \
		$(MAKE) --no-print-directory package VERSION=$(VERSION) GOOS=$${p%/*} GOARCH=$${p#*/} || exit 1; \
	done
	@cd $(DIST_DIR) && $(SHA256SUM) *.tar.gz > checksums.txt
	@ln -sf $(notdir $(BINARY)) $(DIST_DIR)/$(APPLICATION)

.PHONY: version-format
version-format:
	@echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$' || { echo "VERSION $(VERSION) is not a release version: pass VERSION=vX.Y.Z"; exit 1; }

.PHONY: version-tagged
version-tagged: version-format
	@git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null || { echo "no tag $(VERSION): tag the release commit first"; exit 1; }

.PHONY: plugin-version
plugin-version: version-format ## Write VERSION, a release tag, into the plugin manifest
	@sed -i.bak -E 's/"version": "[^"]*"/"version": "$(VERSION)"/' plugin/.claude-plugin/plugin.json
	@rm plugin/.claude-plugin/plugin.json.bak

.PHONY: github-release
github-release: version-tagged ## Create the GitHub release of an existing tag, refused when the plugin manifest names another version
	@grep -q '"version": "$(VERSION)"' plugin/.claude-plugin/plugin.json || { echo "plugin.json is not at $(VERSION): run make plugin-version VERSION=$(VERSION) and commit it first"; exit 1; }
	@gh release create $(VERSION) --generate-notes

.PHONY: release-upload
release-upload: version-tagged ## Attach the release assets to the GitHub release of VERSION
	@gh release view "$(VERSION)" >/dev/null 2>&1 || { echo "no GitHub release $(VERSION): run make github-release first"; exit 1; }
	@$(MAKE) --no-print-directory release-assets VERSION=$(VERSION)
	@gh release upload $(VERSION) $(DIST_DIR)/*.tar.gz $(DIST_DIR)/checksums.txt --clobber

.PHONY: homebrew-formula
homebrew-formula: version-tagged ## Write dist/bellek.rb for VERSION from the checksums of its release
	@mkdir -p $(DIST_DIR)
	@test -f $(DIST_DIR)/checksums.txt || gh release download $(VERSION) --pattern checksums.txt --dir $(DIST_DIR)
	@arm=$$(awk '$$2 == "$(APPLICATION)-darwin-arm64.tar.gz" {print $$1}' $(DIST_DIR)/checksums.txt); \
	intel=$$(awk '$$2 == "$(APPLICATION)-darwin-amd64.tar.gz" {print $$1}' $(DIST_DIR)/checksums.txt); \
	test -n "$$arm" && test -n "$$intel" || { echo "checksums.txt has no darwin tarballs"; exit 1; }; \
	sed -e 's/@TAG@/$(VERSION)/g' -e 's/@VERSION@/$(VERSION:v%=%)/g' -e "s/@SHA256_DARWIN_ARM64@/$$arm/" -e "s/@SHA256_DARWIN_AMD64@/$$intel/" \
		packaging/homebrew/$(APPLICATION).rb.tmpl > $(FORMULA)
	@echo "Formula written: $(FORMULA)"

.PHONY: homebrew-publish
homebrew-publish: homebrew-formula ## Push the formula of VERSION to the Homebrew tap, with HOMEBREW_TAP_TOKEN or your gh login
	@rm -rf $(DIST_DIR)/homebrew-tap
	@export HOMEBREW_TAP_TOKEN="$${HOMEBREW_TAP_TOKEN:-$$(gh auth token)}" GIT_TERMINAL_PROMPT=0 \
		GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_0=credential.helper GIT_CONFIG_VALUE_0= \
		GIT_CONFIG_KEY_1=credential.helper GIT_CONFIG_VALUE_1='!f() { echo username=x-access-token; echo "password=$$HOMEBREW_TAP_TOKEN"; }; f'; \
	git clone --quiet --depth 1 https://github.com/$(TAP_REPOSITORY).git $(DIST_DIR)/homebrew-tap && \
	mkdir -p $(DIST_DIR)/homebrew-tap/Formula && \
	cp $(FORMULA) $(DIST_DIR)/homebrew-tap/Formula/$(APPLICATION).rb && \
	cd $(DIST_DIR)/homebrew-tap && git add Formula/$(APPLICATION).rb && \
	if git diff --cached --quiet; then echo "The tap already has $(VERSION)"; else \
	git -c user.name=utku -c user.email=utku@bellek.si commit --quiet -m "feat: bellek $(VERSION)" && \
	git push --quiet origin HEAD && echo "Pushed $(VERSION) to $(TAP_REPOSITORY)"; fi

.PHONY: release
release: release-upload homebrew-publish ## Run what the release workflow runs, after make github-release
