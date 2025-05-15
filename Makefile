.PHONY:
ztunnel-shim: init-submodule proto
	go build -o $@ ./cmd/ztunnel-shim/...

.PHONY:
proto:
	protoc --go_out=./cni-shim/ --go_opt=paths=source_relative ./ztunnel/proto/zds.proto

.PHONY:
init-submodule:
	git submodule update --init --recursive

.PHONY:
clean:
	rm -rf ./ztunnel-shim
	rm -rf ./cni-shim/ztunnel/
