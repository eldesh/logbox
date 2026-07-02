APP := logbox
VERSION ?= 0.1.0
DIST_DIR := dist
MAN_SRC := docs/man/$(APP).1.md
MAN_DIR := $(DIST_DIR)/man
MAN_OUT := $(MAN_DIR)/$(APP).1
MD2MAN_PKG := github.com/cpuguy83/go-md2man/v2@latest
BIN_ARCHES := amd64 armv6 armv7 arm64
BIN_TARGETS := $(BIN_ARCHES:%=$(DIST_DIR)/$(APP)-linux-%)
DEB_ARCHES := amd64 armhf arm64
DEB_TARGETS := $(DEB_ARCHES:%=$(DIST_DIR)/$(APP)_$(VERSION)_%.deb)

GOARCH_amd64 := amd64
GOARCH_armv6 := arm
GOARCH_armv7 := arm
GOARCH_arm64 := arm64
GOARM_armv6 := 6
GOARM_armv7 := 7

DEB_BIN_ARCH_amd64 := amd64
DEB_BIN_ARCH_armhf := armv6
DEB_BIN_ARCH_arm64 := arm64


GO ?= go
NFPM ?= nfpm
CGO_ENABLED ?= 0
LDFLAGS ?=
PREFIX ?= /usr/local
DESTDIR ?=
MANDIR ?= $(PREFIX)/share/man
MAN1DIR ?= $(MANDIR)/man1

.PHONY: help build install man install-man clean bin deb $(BIN_ARCHES:%=bin-%) $(DEB_ARCHES:%=deb-%) $(BIN_TARGETS) $(DEB_TARGETS)

help:
	@echo "Targets:"
	@echo "  build       Build for current host"
	@echo "  install     Install via 'go install' (set GOBIN to choose destination)"
	@echo "  man         Generate man page at $(MAN_OUT)"
	@echo "  install-man Install man page to $(DESTDIR)$(MAN1DIR)/$(APP).1"
	@echo "  bin         Build all binary targets (amd64, armv6, armv7, arm64)"
	@echo "  bin-amd64   Build for AMD64 (x86_64)"
	@echo "  bin-armv6   Build for Raspberry Pi 1/Zero (32-bit ARMv6)"
	@echo "  bin-armv7   Build for Raspberry Pi 2/3/4/5 with 32-bit OS (ARMv7)"
	@echo "  bin-arm64   Build for Raspberry Pi 3/4/5 with 64-bit OS (ARM64)"
	@echo "  deb         Build deb packages (amd64, armhf with ARMv6 binary, arm64)"
	@echo "  deb-amd64   Build AMD64 deb package"
	@echo "  deb-armhf   Build ARMHF deb package with ARMv6 binary"
	@echo "  deb-arm64   Build ARM64 deb package"
	@echo "  clean       Remove built artifacts"

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o $(APP) .

install:
	$(GO) install -ldflags '$(LDFLAGS)' .

man: $(MAN_OUT)

$(MAN_OUT): $(MAN_SRC)
	mkdir -p $(MAN_DIR)
	$(GO) run $(MD2MAN_PKG) < $(MAN_SRC) > $(MAN_OUT)

install-man: $(MAN_OUT)
	install -d $(DESTDIR)$(MAN1DIR)
	install -m 0644 $(MAN_OUT) $(DESTDIR)$(MAN1DIR)/$(APP).1

bin: bin-amd64 bin-armv6 bin-armv7 bin-arm64

bin-amd64 bin-armv6 bin-armv7 bin-arm64: bin-%: $(DIST_DIR)/$(APP)-linux-%

$(BIN_TARGETS): $(DIST_DIR)/$(APP)-linux-%:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=$(GOARCH_$*) GOARM=$(GOARM_$*) CGO_ENABLED=$(CGO_ENABLED) $(GO) build -ldflags '$(LDFLAGS)' -o $@ .

deb: deb-amd64 deb-armhf deb-arm64

deb-amd64 deb-armhf deb-arm64: deb-%: $(DIST_DIR)/$(APP)_$(VERSION)_%.deb

.SECONDEXPANSION:
$(DEB_TARGETS): $(DIST_DIR)/$(APP)_$(VERSION)_%.deb: $$(DIST_DIR)/$$(APP)-linux-$$(DEB_BIN_ARCH_$$*) $(MAN_OUT) nfpm.yaml
	VERSION=$(VERSION) NFPM_ARCH=$* NFPM_BINARY=$(DIST_DIR)/$(APP)-linux-$(DEB_BIN_ARCH_$*) $(NFPM) package --packager deb --config nfpm.yaml --target $@

clean:
	$(RM) $(APP)
	$(RM) $(BIN_TARGETS)
	$(RM) $(DEB_TARGETS)
	$(RM) $(MAN_OUT)
