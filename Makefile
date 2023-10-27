version := $(shell git describe  --always --tags --long)
buildtime := $(shell date -u +%Y%m%d.%H%M%S)

GOBUILD_OPTS := -v -ldflags "-X main.version=$(version)-$(buildtime)"

.PHONY: all
all: kafka-offset-exporter

.PHONY: kafka-offset-exporter
kafka-offset-exporter: 
	CGO_ENABLED=0 go build $(GOBUILD_OPTS)

# Common
.PHONY: docker
docker: 
	docker build -t kafka-offset-exporter:$(version) .

.PHONY: test
test:
	go test ./...

.PHONY: lint
lint:
	@gometalinter --disable-all --enable=vet --enable=vetshadow  --enable=structcheck \
	    --enable=deadcode --enable=gotype --enable=goconst --enable=golint --enable=varcheck \
	     --enable=unconvert --enable=staticcheck --enable=dupl --enable=ineffassign \
	     --enable=gocyclo --cyclo-over=20 --vendor ./...

.phony clean:
	rm -f ./kafka-offset-exporter