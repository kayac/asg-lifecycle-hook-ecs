VERSION := $(shell git describe --tags)
.PHONY: build build.lambda image test clean

build: *.go go.*
	go build

build.lambda: *.go go.*
	GOARCH=amd64 GOOS=linux go build -o bootstrap

test:
	go clean -testcache
	go test -v -race ./...

clean:
	rm -f asg-lifecycle-hook-ecs bootstrap dist/

image: build.lambda Dockerfile
	docker build \
	  --rm \
	  --tag ghcr.io/kayac/asg-lifecycle-hook-ecs:$(VERSION) \
	  .

push: image
	docker push ghcr.io/kayac/asg-lifecycle-hook-ecs:$(VERSION)
