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
