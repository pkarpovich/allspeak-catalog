SSH_KEY ?= $(HOME)/.ssh/id_ed25519

.PHONY: lint test build

lint:
	golangci-lint run

test:
	go test ./... -race

build:
	CGO_ENABLED=0 go build -o allspeak-catalog ./cmd/allspeak-catalog

deploy_%:
	spot -t $* -v -i ./inventory.yml -k $(SSH_KEY)
