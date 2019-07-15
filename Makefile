version := $(shell git describe  --always --tags --long)
buildtime := $(shell date -u +%Y%m%d.%H%M%S)

GOBUILD_OPTS := -v -mod=vendor -ldflags "-X main.version=$(version)-$(buildtime)"

all: kafka-offset-exporter

kafka-offset-exporter: 
	cd cmd/kafka2eventhub && CGO_ENABLED=0 go build $(GOBUILD_OPTS)

# Common
docker: 
	docker build -t kafka-offset-exporter:$(version) --build-arg REGISTRY_BASE_IMAGES=$(REGISTRY_BASE_IMAGES) .

test:
	go test ./...

lint:
	@gometalinter --disable-all --enable=vet --enable=vetshadow  --enable=structcheck \
	    --enable=deadcode --enable=gotype --enable=goconst --enable=golint --enable=varcheck \
	     --enable=unconvert --enable=staticcheck --enable=dupl --enable=ineffassign \
	     --enable=gocyclo --cyclo-over=20 --vendor ./...

clean:
	rm -f ./kafka-offset-exporter