GOPATH := $(shell pwd)
GOBIN := $(GOPATH)/bin

all: rtpproxy_monitoring

rtpproxy_monitoring:
	GOPATH=$(GOPATH) GOBIN=$(GOPATH)/bin go get
	GOPATH=$(GOPATH) GOBIN=$(GOPATH)/bin go build -v -x

clean:
	GOPATH=$(GOPATH) go clean
	${RM} -r bin/ pkg/

.PHONY: rtpproxy_monitoring
