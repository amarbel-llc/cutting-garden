default: build test

build: build-gomod2nix build-go build-nix

build-go: build-gomod2nix
    nix develop --command go build ./...

build-gomod2nix:
    nix develop --command gomod2nix

build-nix:
    nix build --show-trace

test: test-go test-vet

test-go:
    nix develop --command go test ./...

test-vet:
    nix develop --command go vet ./...

update: update-go update-nix

update-go: && build-gomod2nix
    nix develop --command go mod tidy

update-nix:
    nix flake update

[group('debug')]
debug-build-go:
    nix develop --command go build -o .tmp/cutting-garden ./cmd/cutting-garden

[group('debug')]
debug-make-fixture:
    rm -rf .tmp/cap-fixture
    mkdir -p .tmp/cap-fixture/nested
    printf 'hello cutting-garden\n' > .tmp/cap-fixture/hello.txt
    printf 'nested content\n'       > .tmp/cap-fixture/nested/inner.txt

[group('debug')]
debug-capture-fixture STORE='.default' FORMAT='auto': debug-build-go debug-make-fixture
    .tmp/cutting-garden capture -format={{FORMAT}} {{STORE}} .tmp/cap-fixture

[group('debug')]
debug-capture-fixture-nix STORE='.default' FORMAT='auto': build-nix debug-make-fixture
    ./result/bin/cutting-garden capture -format={{FORMAT}} {{STORE}} .tmp/cap-fixture

[group('debug')]
debug-madder-init STORE='.test':
    nix develop --command madder init {{STORE}}

[group('debug')]
debug-make-multiroot-fixture: debug-make-fixture
    rm -rf .tmp/cap-fixture-2
    mkdir -p .tmp/cap-fixture-2/sub
    printf 'second root\n' > .tmp/cap-fixture-2/top.txt
    printf 'inner two\n'   > .tmp/cap-fixture-2/sub/inner.txt

[group('debug')]
debug-capture-multiroot STORE='.default' FORMAT='auto': debug-build-go debug-make-multiroot-fixture
    .tmp/cutting-garden capture -format={{FORMAT}} {{STORE}} .tmp/cap-fixture .tmp/cap-fixture-2
