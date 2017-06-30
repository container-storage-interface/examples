SHELL := $(shell env which bash)

all: build

# load the go bits
include go.mk

# configure and load the csi protobuf generator
CSI_PROTO_DIR := csi
include csi.mk

# the name of the program being built
PROG := $(PKG_DIR_GO)/$(IMPORT_PATH).a

# the target for building the gocsi library
$(PROG):$(CSI_GOSRC) \
		$(filter-out %_test.go,$(wildcard *.go)) \
		| $(GOGET) $(PKG_DIR_GO)
	go install -pkgdir $(abspath $(PKG_DIR_GO))
	@echo $@

# a list of sub-projects to make
SUB_PROJS := $(subst /,,$(dir $(wildcard */Makefile)))

# prints a list of the projects to make
projs:
	@echo . $(SUB_PROJS)

build: $(PROG)
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

clean:
	go clean -i
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

clobber: clean
	rm -fr $(CSI_PROTO_DIR) $(BUILD_DIR)
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

goget: $(GOGET)
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

test: build
	@rm -f csd.log
	CSI_PLUGINS=$$(pwd)/csp/moc/moc-csi-plugin.so csd/csd mock > csd.log 2>&1 &
	@until grep 'mock\.Serve' csd.log > /dev/null 2>&1; do sleep 0.1; done;
	csc/csc ls  -version 0.1.0
	csc/csc new -version 0.1.0 \
	            -o norootsquash,uid=500,gid=500 \
                -t ext4 -requiredBytes 107374182400 \
                -params color=purple,up=down \
                "My New Volume"
	csc/csc ls  -version 0.1.0
	kill $$(ps aux | grep '[c]sd' | awk '{print $$2}')
	cat csd.log

benchmark: $(PROG)
	go test -benchmem -parallel 100 -bench .

.PHONY: projs build clean clobber goget vendor test benchmark
