# job-chat-copilot server 构建脚本
# 产物：单二进制（darwin/amd64），内嵌控制台静态资源（存在时）

BINARY := bin/job-chat-copilot
GOOS   := darwin
GOARCH := amd64
DIST_DIR := internal/webui/dist
WEB_DIST := ../web/dist

.PHONY: build test vet clean web-dist

## build: 打包 darwin/amd64 单二进制（先内嵌 web/dist，若存在）
build: web-dist
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "-s -w" -o $(BINARY) ./cmd/job-chat-copilot

## web-dist: 若 ../web/dist 存在，则拷贝覆盖 embed 目录（不存在则保留降级占位页）
web-dist:
	@if [ -d "$(WEB_DIST)" ]; then \
		rm -rf $(DIST_DIR) && cp -r $(WEB_DIST) $(DIST_DIR); \
		echo "embedded web/dist into $(DIST_DIR)"; \
	else \
		echo "warn: $(WEB_DIST) not found, embedding fallback page only"; \
	fi

## dev: 本机架构快速构建（不内嵌前端，用于开发）
dev:
	go build -o $(BINARY) ./cmd/job-chat-copilot

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	printf '<!doctype html><html lang="zh-CN"><body>控制台前端尚未构建。</body></html>' > $(DIST_DIR)/index.html
