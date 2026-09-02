.PHONY: build web test

build: web
	go build -o ohyeah .

web:
	npm run build

test:
	npm run check
	go test ./...
