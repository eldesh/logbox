APP := logbox
DIST_DIR := dist
MAN_SRC := docs/man/$(APP).1.md
MAN_DIR := $(DIST_DIR)/man
MAN_OUT := $(MAN_DIR)/$(APP).1
MD2MAN_PKG := github.com/cpuguy83/go-md2man/v2@latest
RPI_ARCHES := armv6 armv7 arm64
RPI_TARGETS := $(RPI_ARCHES:%=$(DIST_DIR)/$(APP)-linux-%)


GO ?= go
CGO_ENABLED ?= 0
LDFLAGS ?=
PREFIX ?= /usr/local
DESTDIR ?=
MANDIR ?= $(PREFIX)/share/man
MAN1DIR ?= $(MANDIR)/man1

.PHONY: help build install man install-man clean rpi rpi-armv6 rpi-armv7 rpi-arm64 $(RPI_TARGETS)

help:
	@echo "Targets:"
	@echo "  build       Build for current host"
	@echo "  install     Install via 'go install' (set GOBIN to choose destination)"
	@echo "  man         Generate man page at $(MAN_OUT)"
	@echo "  install-man Install man page to $(DESTDIR)$(MAN1DIR)/$(APP).1"
	@echo "  rpi         Build all Raspberry Pi targets (armv6, armv7, arm64)"
	@echo "  rpi-armv6   Build for Raspberry Pi 1 / Zero (32-bit ARMv6)"
	@echo "  rpi-armv7   Build for Raspberry Pi 2/3/4/5 with 32-bit OS (ARMv7)"
	@echo "  rpi-arm64   Build for Raspberry Pi 3/4/5 with 64-bit OS (ARM64)"
	@echo "  clean       Remove built artifacts"

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o $(APP) .

install:
	$(GO) install -ldflags '$(LDFLAGS)' .

man:
	mkdir -p $(MAN_DIR)
	$(GO) run $(MD2MAN_PKG) < $(MAN_SRC) > $(MAN_OUT)

install-man: man
	install -d $(DESTDIR)$(MAN1DIR)
	install -m 0644 $(MAN_OUT) $(DESTDIR)$(MAN1DIR)/$(APP).1

rpi: rpi-armv6 rpi-armv7 rpi-arm64

rpi-armv6: GOARCH=arm GOARM=6
rpi-armv7: GOARCH=arm GOARM=7
rpi-arm64: GOARCH=arm64

rpi-armv6 rpi-armv7 rpi-arm64: rpi-%: $(DIST_DIR)/$(APP)-linux-%

$(RPI_TARGETS): $(DIST_DIR)/$(APP)-linux-%:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=$(GOARCH) GOARM=$(GOARM) CGO_ENABLED=$(CGO_ENABLED) $(GO) build -ldflags '$(LDFLAGS)' -o $@ .

clean:
	$(RM) $(APP)
	$(RM) $(RPI_TARGETS)
	$(RM) $(MAN_OUT)
