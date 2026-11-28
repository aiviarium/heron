# Shortcuts for the scripts that build, install and try heron. The scripts
# are the source of truth; each target runs one.

PREFIX ?= $(HOME)/.local
EXAMPLE ?= petstore

.PHONY: build install play test clean

# bin/heron, with the version stamped.
build:
	scripts/build.sh

# bin/heron, copied to $(PREFIX)/bin.
install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/heron $(PREFIX)/bin/heron

# heron serve on a scratch copy of examples/$(EXAMPLE).
play:
	scripts/play.sh $(EXAMPLE)

# The whole check, as CI runs it.
test:
	scripts/ci.sh

clean:
	rm -rf bin .heron/bin
