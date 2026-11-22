# kore-plugin-monitor 构建入口：build / dist / check / clean。
SHELL := /bin/bash
export GOWORK := off

ID := monitor
BIN := bin/$(ID)
DIST := dist
VERSION := $(shell sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' info.json | head -n 1)
PACKAGE := $(DIST)/$(ID)-$(VERSION).tar.gz

.PHONY: build dist check clean

# 静态构建：G-16 兼容性基线（CGO_ENABLED=0 + GOAMD64=v1 + trimpath + -s -w）。
build:
	CGO_ENABLED=0 GOAMD64=v1 go build -trimpath -buildvcs=false -ldflags="-s -w" -o $(BIN) .

# 确定性打包：显式列条目（info.json + bin/monitor + SHA256SUMS），无 ./ 根条目。
# --sort=name + --mtime=@0 + 固定 owner/group + gzip -9n 保证同源码两次出包 sha 一致。
# size_bytes 用实测 .tar.gz 体积回写并迭代收敛，与包体积偏差远小于 10%（S-03）。
dist: build
	@set -eo pipefail; \
	rm -rf $(DIST)/stage $(PACKAGE) $(PACKAGE).sha256; \
	mkdir -p $(DIST)/stage/bin; \
	cp $(BIN) $(DIST)/stage/bin/$(ID); \
	actual=0; \
	for _ in 1 2 3 4 5 6 7 8; do \
		sed "s/\"size_bytes\": 0/\"size_bytes\": $$actual/" info.json > $(DIST)/stage/info.json; \
		( cd $(DIST)/stage && sha256sum info.json bin/$(ID) > SHA256SUMS ); \
		tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - -C $(DIST)/stage info.json bin SHA256SUMS | gzip -9n > $(PACKAGE); \
		next=$$(stat -c%s $(PACKAGE)); \
		if [ "$$next" = "$$actual" ]; then break; fi; \
		actual=$$next; \
	done; \
	sed "s/\"size_bytes\": 0/\"size_bytes\": $$actual/" info.json > $(DIST)/stage/info.json; \
	( cd $(DIST)/stage && sha256sum info.json bin/$(ID) > SHA256SUMS ); \
	tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - -C $(DIST)/stage info.json bin SHA256SUMS | gzip -9n > $(PACKAGE); \
	( cd $(DIST) && sha256sum $(ID)-$(VERSION).tar.gz > $(ID)-$(VERSION).tar.gz.sha256 ); \
	rm -rf $(DIST)/stage; \
	echo "打包完成: $(PACKAGE) size_bytes=$$actual"

# 门禁：gofmt（有输出即失败）→ go vet → go test。
check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt 需要格式化："; echo "$$out"; exit 1; fi; echo "gofmt: clean"
	go vet ./...
	go test -timeout 200s ./...

clean:
	rm -rf bin dist
	rm -f go.work.sum
