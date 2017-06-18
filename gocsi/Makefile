# the name of the program being built
PROG := gocsi

# adjust the server & client binary names for windows
ifeq (windows,$(GOOS))
PROG := $(PROG).exe
endif

all: $(PROG)

# configure and load the csi protobuf generator
CSI_PROTO_DIR := csi
include csi.mk

$(PROG): $(CSI_GOSRC) main.go
	go build -o $@

clean:
	rm -f $(PROG)

clobber: clean
	rm -f $(CSI_PROTO) $(CSI_GOSRC)

.PHONY: clean clobber
