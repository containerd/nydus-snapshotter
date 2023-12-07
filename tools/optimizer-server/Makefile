OS ?= linux
ARCH ?= amd64

arch_amd64 := x86_64
arch_arm64 := aarch64
arch_ppc64le := powerpc64le
arch_riscv64 := riscv64gc

linker_amd64 := x86_64
linker_arm64 := aarch64
linker_ppc64le := powerpc64le

ifneq ($(arch_$(ARCH)),)
    RUST_ARCH := $(arch_$(ARCH))
else
    RUST_ARCH := $(ARCH)
endif

ifneq ($(linker_$(ARCH)),)
    RUST_LINKER := $(linker_$(ARCH))
else
    RUST_LINKER := $(ARCH)
endif

RUST_TARGET := $(RUST_ARCH)-unknown-$(OS)-gnu
RUST_LINKER := $(RUST_LINKER)-$(OS)-gnu-gcc
RUST_TYPE := debug

all: build

.PHONY: .release_version .format build release

.release_version:
	$(eval CARGO_BUILD_FLAGS += --release)
	$(eval RUST_TYPE := release)
	$(eval RUST_FLAGS += -C target-feature=+crt-static -C strip=symbols)

.format:
	cargo fmt -- --check

build: .format
	rustup target add $(RUST_TARGET)
	RUSTFLAGS="-C linker=$(RUST_LINKER) $(RUST_FLAGS)" cargo build $(CARGO_BUILD_FLAGS) --target $(RUST_TARGET)
	cargo clippy $(CARGO_BUILD_FLAGS) -- -Dwarnings
	install -D -m 755 target/$(RUST_TARGET)/$(RUST_TYPE)/optimizer-server bin/optimizer-server

release: .format .release_version build

static-release: .format 
	cargo clippy $(CARGO_BUILD_FLAGS) -- -Dwarnings
	RUSTFLAGS="-C linker=$(RUST_LINKER) -C target-feature=+crt-static -C target-feature=+crt-static -C strip=symbols" cargo build --release --target $(RUST_TARGET)
	install -D -m 755 target/$(RUST_TARGET)/release/optimizer-server bin/optimizer-server

clean:
	cargo clean
	rm -rf bin/*