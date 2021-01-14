.PHONY: build test clean

build:
	go build

test:
	go clean -testcache
	go test -v -race ./...

clean:
	rm -f asg-lifecycle-hook-ecs dist/
