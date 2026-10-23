.PHONY: test build smoke

test:
	go test -race -count=1 ./...
	go vet ./...

build:
	mkdir -p bin
	go build -trimpath -o bin/mousebridge-macos-arm64 ./cmd/mousebridge
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -trimpath -o bin/mousebridge-macos-amd64 ./cmd/mousebridge
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/mousebridge-windows-amd64.exe ./cmd/mousebridge
	GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o bin/mousebridge-windows-arm64.exe ./cmd/mousebridge

smoke:
	python3 scripts/smoke.py
