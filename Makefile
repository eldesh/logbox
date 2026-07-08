APP         := logbox
DEFAULT_VERSION := $(shell ./scripts/version.sh 2>/dev/null || echo 0.1.0~dev.0+gunknown)
VERSION     ?= $(DEFAULT_VERSION)
DIST_DIR    := dist
MAN_SRC     := docs/man/$(APP).1.md
MAN_DIR     := $(DIST_DIR)/man
MAN_OUT     := $(MAN_DIR)/$(APP).1
MD2MAN_VER  ?= v2.0.7
NFPM_VER    ?= v2.47.0
GOLANGCI_LINT_VER ?= v2.4.0
MD2MAN_PKG  := github.com/cpuguy83/go-md2man/v2@$(MD2MAN_VER)
NFPM_PKG    := github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VER)
GOLANGCI_LINT_PKG := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VER)
BIN_ARCHES  := amd64 armv6 armv7 arm64
BIN_TARGETS := $(BIN_ARCHES:%=$(DIST_DIR)/$(APP)-linux-%)
DEB_ARCHES  := amd64 armhf arm64
DEB_TARGETS := $(DEB_ARCHES:%=$(DIST_DIR)/$(APP)_$(VERSION)_%.deb)
PKGROOT     := $(DIST_DIR)/pkgroot

GO          ?= go
GOFMT       ?= gofmt
CGO_ENABLED ?= 0
LDFLAGS     ?=
PREFIX      ?= /usr/local
DESTDIR     ?=
MANDIR      ?= $(PREFIX)/share/man
MAN1DIR     ?= $(MANDIR)/man1

.PHONY: help build install fmt fmt-check lint man install-man clean bin deb $(BIN_ARCHES:%=bin-%) $(DEB_ARCHES:%=deb-%) $(BIN_TARGETS) $(DEB_TARGETS)

help:
	@echo "Targets:"
	@echo "  build       Build for current host"
	@echo "  install     Install via 'go install' (set GOBIN to choose destination)"
	@echo "  fmt         Format Go files with gofmt"
	@echo "  fmt-check   Verify Go files are gofmt-formatted"
	@echo "  lint        Run golangci-lint"
	@echo "  man         Generate man page at $(MAN_OUT)"
	@echo "  install-man Install man page to $(DESTDIR)$(MAN1DIR)/$(APP).1"
	@echo "  bin         Build all binary targets (amd64, armv6, armv7, arm64)"
	@echo "  bin-amd64   Build for amd64 (x86_64)"
	@echo "  bin-armv6   Build for 32-bit armv6"
	@echo "  bin-armv7   Build for 32-bit armv7"
	@echo "  bin-arm64   Build for 64-bit arm64"
	@echo "  deb         Build deb packages (amd64, armhf with ARMv6 binary, arm64)"
	@echo "  deb-amd64   Build amd64 deb package"
	@echo "  deb-armhf   Build armhf deb package for Raspberry Pi Zero/1/2/3/4/5 with 32-bit OS"
	@echo "  deb-arm64   Build arm64 deb package for Raspberry Pi 3/4/5 with 64-bit OS"
	@echo "  clean       Remove built artifacts"

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o $(APP) .

install:
	$(GO) install -ldflags '$(LDFLAGS)' .

fmt:
	@$(GOFMT) -w .

fmt-check:
	@unfmt=$$($(GOFMT) -l .); \
	if [ -n "$$unfmt" ]; then \
		echo "$$unfmt"; \
		exit 1; \
	fi

lint:
	$(GO) run $(GOLANGCI_LINT_PKG) run --timeout=5m

man: $(MAN_OUT)

$(MAN_OUT): $(MAN_SRC)
	mkdir -p $(MAN_DIR)
	$(GO) run $(MD2MAN_PKG) < $(MAN_SRC) > $(MAN_OUT)

install-man: $(MAN_OUT)
	install -d $(DESTDIR)$(MAN1DIR)
	install -m 0644 $(MAN_OUT) $(DESTDIR)$(MAN1DIR)/$(APP).1

bin: bin-amd64 bin-armv6 bin-armv7 bin-arm64

bin-amd64 bin-armv6 bin-armv7 bin-arm64: bin-%: $(DIST_DIR)/$(APP)-linux-%


# Parameters:
#   $(1): Go target architecture.
#        This value is passed to go build as GOARCH.
#        Variant: amd64, arm, arm64
#
#   $(2): Go ARM version.
#        This value is passed to go build as GOARM for 32-bit ARM builds.
#        Leave empty for non-ARM and arm64 builds.
#        Variant: empty, 6, 7
define build_bin
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=$(1) GOARM=$(2) CGO_ENABLED=$(CGO_ENABLED) $(GO) build -ldflags '$(LDFLAGS)' -o $@ .
endef

$(DIST_DIR)/$(APP)-linux-amd64:
	$(call build_bin,amd64,)

$(DIST_DIR)/$(APP)-linux-armv6:
	$(call build_bin,arm,6)

$(DIST_DIR)/$(APP)-linux-armv7:
	$(call build_bin,arm,7)

$(DIST_DIR)/$(APP)-linux-arm64:
	$(call build_bin,arm64,)


deb: deb-amd64 deb-armhf deb-arm64

deb-amd64 deb-armhf deb-arm64: deb-%: $(DIST_DIR)/$(APP)_$(VERSION)_%.deb

# Parameters:
#   $(1): Debian package architecture.
#        This value is passed to nfpm as PKG_ARCH and is used as the
#        Architecture field of the generated .deb package.
#        Variant: amd64, arm64, armhf
#
#   $(2): Binary architecture suffix.
#        This value selects the already-built binary:
#          $(DIST_DIR)/$(APP)-linux-$(2)
#        and installs it into the package root as:
#          $(PKGROOT)/usr/bin/$(APP)-linux-$(2)
#        Variant: amd64, arm64, armv6
define build_deb
	mkdir -p $(PKGROOT)/usr/bin $(PKGROOT)/usr/share/man/man1
	cp $(DIST_DIR)/$(APP)-linux-$(2) $(PKGROOT)/usr/bin/$(APP)-linux-$(2)
	cp $(MAN_OUT) $(PKGROOT)/usr/share/man/man1/$(APP).1
	VERSION=$(VERSION) PKG_ARCH=$(1) PKG_BIN=$(PKGROOT)/usr/bin/$(APP)-linux-$(2) $(GO) run $(NFPM_PKG) package --packager deb --config nfpm.yaml --target $@
endef

$(DIST_DIR)/$(APP)_$(VERSION)_amd64.deb: bin-amd64 $(MAN_OUT) nfpm.yaml
	$(call build_deb,amd64,amd64)

$(DIST_DIR)/$(APP)_$(VERSION)_arm64.deb: bin-arm64 $(MAN_OUT) nfpm.yaml
	$(call build_deb,arm64,arm64)

$(DIST_DIR)/$(APP)_$(VERSION)_armhf.deb: bin-armv6 $(MAN_OUT) nfpm.yaml
	$(call build_deb,armhf,armv6)

clean:
	$(RM) $(APP)
	$(RM) $(BIN_TARGETS)
	$(RM) $(DEB_TARGETS)
	$(RM) $(MAN_OUT)
	$(RM) -r $(PKGROOT)
