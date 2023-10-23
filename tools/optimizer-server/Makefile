TARGET_ARCH ?= x86_64
EXTRA_RUSTFLAGS ?=

ifeq ($(TARGET_ARCH), s390x)
        override EXTRA_RUSTFLAGS += -C linker=s390x-linux-gnu-gcc
endif

all: build

.PHONY: .release_version .format build release

.release_version:
	$(eval CARGO_BUILD_FLAGS += --release)

.format:
	cargo fmt -- --check

build: .format
	cargo build $(CARGO_BUILD_FLAGS)
	cargo clippy $(CARGO_BUILD_FLAGS) -- -Dwarnings

release: .format .release_version build

static-release: .format 
	cargo clippy $(CARGO_BUILD_FLAGS) -- -Dwarnings
	RUSTFLAGS="-C target-feature=+crt-static ${EXTRA_RUSTFLAGS}" cargo build --release --target ${TARGET_ARCH}-unknown-linux-gnu

clean:
	cargo clean
