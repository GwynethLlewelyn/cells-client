DEV_VERSION=2.2.2-dev
ENV=env GOOS=linux
TODAY:=$(shell date -u +%Y-%m-%dT%H:%M:%S)
TIMESTAMP:=$(shell date -u +%Y%m%d%H%M%S)
GITREV?=$(shell git rev-parse HEAD)
CELLS_CLIENT_VERSION?=${DEV_VERSION}.${TIMESTAMP}

XGO_TARGETS?="linux/amd64,darwin/amd64,windows/amd64"
XGO_IMAGE?=techknowlogick/xgo:go-1.19.x
XGO_BIN?=${GOPATH}/bin/xgo

.PHONY: all clean main dev xgo

main:
	go build -a\
	 -ldflags "-X github.com/pydio/cells-client/v2/common.Version=${CELLS_CLIENT_VERSION}\
	 -X github.com/pydio/cells-client/v2/common.BuildStamp=${TODAY}\
	 -X github.com/pydio/cells-client/v2/common.BuildRevision=${GITREV}"\
	 -o cec\
	 .

xgo:
	${XGO_BIN} -go 1.19 \
	 --image ${XGO_IMAGE} \
	 --targets ${XGO_TARGETS} \
	 -ldflags "-X github.com/pydio/cells-client/v2/common.Version=${CELLS_CLIENT_VERSION}\
	 -X github.com/pydio/cells-client/v2/common.BuildStamp=${TODAY}\
	 -X github.com/pydio/cells-client/v2/common.BuildRevision=${GITREV}"\
	 -out cec\
	 .

dev:
	go build \
	 -tags dev \
	 -ldflags "-X github.com/pydio/cells-client/v2/common.Version=${DEV_VERSION}\
	 -X github.com/pydio/cells-client/v2/common.BuildStamp=2022-01-01T00:00:00\
	 -X github.com/pydio/cells-client/v2/common.BuildRevision=dev"\
	 -o cec\
	 .

clean:
	rm -f cec
