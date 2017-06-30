SHELL := $(shell env which bash)

all: build

# load the go bits
include ../go.mk

# configure and load the csi protobuf generator
CSI_PROTO_DIR := ../csi
include ../csi.mk

# the name of the program being built
PROG := $(notdir $(shell pwd))

# the program to build
PROG_BIN := $(PROG)

# adjust the binary names for windows
ifeq (windows,$(GOOS))
PROG_BIN := $(PROG_BIN).exe
endif

PROG_BIN_TMP := $(BIN_DIR_GO)/$(PROG)
$(PROG_BIN_TMP): main.go $(CSI_GOSRC) | $(GOGET) $(BIN_DIR_GO)
	GOBIN=$(abspath $(@D)) go install
$(PROG_BIN): $(PROG_BIN_TMP)
	cp -f $< $@

build: $(PROG_BIN)

clean:
	go clean -i && rm -fr $(PROG_BIN) $(PROG_BIN_TMP)

clobber: clean
	rm -fr $(BUILD_DIR)

.PHONY: clean clobber
