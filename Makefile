GC=go build
COMMIT ?= main

poly-validator: $(SRC_FILES)
	$(GC)  -tags mainnet -o poly-validator main.go

clean:
	rm -f poly-validator
	docker container rm -f go-poly-validator-temp
	docker rmi -f go-poly-validator-build

build: clean
	@echo "Building poly validator binary in container"
	docker build --no-cache --build-arg commit=$(COMMIT) -t go-poly-validator-build .
	docker container create --name go-poly-validator-temp go-poly-validator-build
	docker container cp go-poly-validator-temp:/workspace/poly-validator .
	sha256sum poly-validator

always:
.DELETE_ON_ERROR:
.PHONY: clean