SHELL := $(shell env which bash)

all: build

# a list of sub-projects to make
SUB_PROJS := $(subst /,,$(dir $(wildcard */Makefile)))

# prints a list of the projects to make
projs:
	@echo . $(SUB_PROJS)

build:
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

clean:
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

clobber:
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

goget:
	$(foreach d,$(SUB_PROJS),$(MAKE) -C $d $@;)

.PHONY: projs build clean clobber goget
