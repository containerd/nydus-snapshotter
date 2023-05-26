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
	RUSTFLAGS="-C target-feature=+crt-static" cargo build --release --target x86_64-unknown-linux-gnu

clean:
	cargo clean