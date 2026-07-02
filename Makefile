APP := logbox
DIST_DIR := dist
MAN_SRC := docs/man/$(APP).1.md
MAN_DIR := $(DIST_DIR)/man
MAN_OUT := $(MAN_DIR)/$(APP).1
MD2MAN_PKG := github.com/cpuguy83/go-md2man/v2@latest
BIN_ARCHES := amd64 armv6 armv7 arm64
BIN_TARGETS := $(BIN_ARCHES:%=$(DIST_DIR)/$(APP)-linux-%)


GO ?= go
CGO_ENABLED ?= 0
LDFLAGS ?=
PREFIX ?= /usr/local
DESTDIR ?=
MANDIR ?= $(PREFIX)/share/man
MAN1DIR ?= $(MANDIR)/man1

.PHONY: help build install man install-man clean bin $(BIN_ARCHES:%=bin-%) $(BIN_TARGETS)

help:
	@echo "Targets:"
	@echo "  build       Build for current host"
	@echo "  install     Install via 'go install' (set GOBIN to choose destination)"
	@echo "  man         Generate man page at $(MAN_OUT)"
	@echo "  install-man Install man page to $(DESTDIR)$(MAN1DIR)/$(APP).1"
	@echo "  bin         Build all binary targets (amd64, armv6, armv7, arm64)"
	@echo "  bin-amd64   Build for AMD64 (x86_64)"
	@echo "  bin-armv6   Build for 32-bit ARMv6"
	@echo "  bin-armv7   Build for 32-bit ARMv7"
	@echo "  bin-arm64   Build for 64-bit ARM64"
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

bin: bin-amd64 bin-armv6 bin-armv7 bin-arm64

bin-amd64: GOARCH=amd64
bin-armv6: GOARCH=arm GOARM=6
bin-armv7: GOARCH=arm GOARM=7
bin-arm64: GOARCH=arm64

bin-amd64 bin-armv6 bin-armv7 bin-arm64: bin-%: $(DIST_DIR)/$(APP)-linux-%

$(BIN_TARGETS): $(DIST_DIR)/$(APP)-linux-%:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=$(GOARCH) GOARM=$(GOARM) CGO_ENABLED=$(CGO_ENABLED) $(GO) build -ldflags '$(LDFLAGS)' -o $@ .

clean:
	$(RM) $(APP)
	$(RM) $(BIN_TARGETS)
	$(RM) $(MAN_OUT)
