GO     ?= go
PKGS   := ./...
BENCH  ?= .

.PHONY: check fmt fmt-check vet test bench inspect

## check: 质量门(fmt + vet + test), 提交前必须过
check: fmt-check vet test

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then printf '未格式化的文件:\n%s\n' "$$out"; exit 1; fi

vet:
	$(GO) vet $(PKGS)

test:
	$(GO) test -count=1 $(PKGS)

## bench: 基准与分配数(性能回归的裁判)
bench:
	$(GO) test -run '^$$' -bench $(BENCH) -benchmem $(PKGS)

## inspect: 看每个包的公开 API
inspect:
	@for p in . raster text theme budget encode; do echo "===== $$p"; $(GO) doc ./$$p 2>/dev/null | sed -n '1,40p'; done
