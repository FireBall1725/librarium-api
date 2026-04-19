.PHONY: docs build

docs:
	swag init -g cmd/api/main.go -o docs --parseDependency --parseInternal

build: docs
	go build ./...
