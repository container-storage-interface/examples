SHELL := $(shell env which bash)

all: build

# load the go build logic from the parent directory
include ../../go.mk

# configure and load the csi protobuf generator
CSI_PROTO_DIR := csi
include ../../csi.mk

# the name of the program being built
PROG := $(notdir $(shell pwd))

# the program to build
SERVER_BIN := $(PROG)-csi-server

# adjust the binary names for windows
ifeq (windows,$(GOOS))
SERVER_BIN := $(SERVER_BIN).exe
else
# the name of the plug-in binary to build
PLUGIN_BIN := $(PROG)-csi-plugin.so
endif

# copy some common go sources from the gocsi project
# into this directory. the reason the package containing
# the sources isn't simply imported is due to go plug-ins
COMMON_GO := utils.go errors.go
COMMON_GO_CSI_PKG_SRC := "$(GOCSI_IMPORT_PATH)/csi"
COMMON_GO_CSI_PKG_TGT := "$(IMPORT_PATH)/csi"
COMMON_GO_PKG_SRC := package gocsi
COMMON_GO_PKG_TGT := package main
$(COMMON_GO): %.go: ../../%.go
	sed -e 's@$(COMMON_GO_PKG_SRC)@$(COMMON_GO_PKG_TGT)@' \
	    -e 's@$(COMMON_GO_CSI_PKG_SRC)@$(COMMON_GO_CSI_PKG_TGT)@' \
		$< > $@

SERVER_BIN_TMP := $(BIN_DIR_GO)/$(PROG)
$(SERVER_BIN_TMP):	main.go $(CSI_GOSRC) $(COMMON_GO) \
					| $(BIN_DIR_GO) $(GOGET) $(VENDOR)
	GOBIN=$(abspath $(@D)) go install
$(SERVER_BIN): $(SERVER_BIN_TMP)
	cp -f $< $@

PLUGIN_BIN_TMP := $(PKG_DIR_GO)/$(IMPORT_PATH).a
$(PLUGIN_BIN_TMP):	main.go $(CSI_GOSRC) $(COMMON_GO) \
					| $(PKG_DIR_GO) $(GOGET) $(VENDOR)
	go install -buildmode plugin -pkgdir $(abspath $(PKG_DIR_GO))
$(PLUGIN_BIN): $(PLUGIN_BIN_TMP)
	cp -f $< $@

build: $(SERVER_BIN)
# add the plug-in target to all if building on linux for linux
ifeq (linux_linux,$(GOOS)_$(GOHOSTOS))
build: $(PLUGIN_BIN)
endif

generated: $(COMMON_GO)

clean:
	rm -f $(SERVER_BIN) $(SERVER_BIN_TMP) $(PLUGIN_BIN) $(PLUGIN_BIN_TMP)

clobber: clean
	rm -fr $(BUILD_DIR) $(CSI_PROTO_DIR) $(VENDOR)

.PHONY: clean clobber
