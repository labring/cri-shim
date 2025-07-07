# Change these variables as necessary.
MAIN_PACKAGE_PATH := ./cmd/main.go
BINARY_NAME := cri-shim

# Version management
VERSION_FILE := VERSION
VERSION ?= $(shell if [ -f $(VERSION_FILE) ]; then cat $(VERSION_FILE); else git describe --tags --always --dirty 2>/dev/null || echo "dev"; fi)
COMMIT_HASH ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X github.com/labring/cri-shim/pkg/infoutil.Version=$(VERSION) -X github.com/labring/cri-shim/pkg/infoutil.CommitHash=$(COMMIT_HASH) -X github.com/labring/cri-shim/pkg/infoutil.BuildTime=$(BUILD_TIME)"

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' |  sed -e 's/^/ /'

.PHONY: confirm
confirm:
	@echo -n 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

.PHONY: no-dirty
no-dirty:
	git diff --exit-code

## version: show current version information
.PHONY: version
version:
	@echo "Version: $(VERSION)"
	@echo "Commit Hash: $(COMMIT_HASH)"
	@echo "Build Time: $(BUILD_TIME)"

## version/set: set version in VERSION file
.PHONY: version/set
version/set:
	@if [ -z "$(VER)" ]; then \
		echo "Usage: make version/set VER=1.2.3"; \
		exit 1; \
	fi
	@echo "$(VER)" > $(VERSION_FILE)
	@echo "Version set to $(VER)"

## version/bump: bump version (patch, minor, major)
.PHONY: version/bump
version/bump:
	@if [ -z "$(TYPE)" ]; then \
		echo "Usage: make version/bump TYPE=patch|minor|major"; \
		exit 1; \
	fi
	@if [ ! -f $(VERSION_FILE) ]; then \
		echo "1.0.0" > $(VERSION_FILE); \
	fi
	@current=$$(cat $(VERSION_FILE)); \
	major=$$(echo $$current | cut -d. -f1); \
	minor=$$(echo $$current | cut -d. -f2); \
	patch=$$(echo $$current | cut -d. -f3); \
	case "$(TYPE)" in \
		patch) new_version="$$major.$$minor.$$((patch + 1))" ;; \
		minor) new_version="$$major.$$((minor + 1)).0" ;; \
		major) new_version="$$((major + 1)).0.0" ;; \
		*) echo "Invalid TYPE. Use patch, minor, or major"; exit 1 ;; \
	esac; \
	echo $$new_version > $(VERSION_FILE); \
	echo "Version bumped to $$new_version"

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## tidy: format code and tidy modfile
.PHONY: tidy
tidy:
	go fmt ./...
	go mod tidy -v

## audit: run quality control checks
.PHONY: audit
audit:
	go mod verify
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@latest -checks=all,-ST1000,-U1000 ./...
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	go test -race -buildvcs -vet=off ./...


# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## test: run all tests
.PHONY: test
test:
	go test -v -race -buildvcs ./...

## test/cover: run all tests and display coverage
.PHONY: test/cover
test/cover:
	go test -v -race -buildvcs -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out

## build: build the application
.PHONY: build
build:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o=./bin/${BINARY_NAME} ${MAIN_PACKAGE_PATH}

## build/release: build the application for release with version info
.PHONY: build/release
build/release:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o=./bin/${BINARY_NAME}-$(VERSION) ${MAIN_PACKAGE_PATH}

## run: run the  application
.PHONY: run
run: build
	/tmp/bin/${BINARY_NAME}

## run/live: run the application with reloading on file changes
.PHONY: run/live
run/live:
	go run github.com/cosmtrek/air@v1.43.0 \
	--build.cmd "make build" --build.bin "/tmp/bin/${BINARY_NAME}" --build.delay "100" \
	--build.exclude_dir "" \
	--build.include_ext "go, tpl, tmpl, html, css, scss, js, ts, sql, jpeg, jpg, gif, png, bmp, svg, webp, ico" \
	--misc.clean_on_exit "true"

# ==================================================================================== #
# OPERATIONS
# ==================================================================================== #

## push: push changes to the remote Git repository
.PHONY: push
push: tidy audit no-dirty
	git push

## production/deploy: deploy the application to production
.PHONY: production/deploy
production/deploy: confirm tidy audit no-dirty
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -ldflags='-s' -o=/tmp/bin/linux_amd64/${BINARY_NAME} ${MAIN_PACKAGE_PATH}
	upx -5 /tmp/bin/linux_amd64/${BINARY_NAME}
	# Include additional deployment steps here...

systemd/install:
	sudo mkdir -p /var/run/sealos
	sudo cp ./systemd/cri-shim.service /etc/systemd/system/cri-shim.service
	sudo systemctl daemon-reload
	sudo systemctl enable cri-shim
	sudo systemctl start cri-shim

systemd/update: build
	sudo systemctl stop cri-shim
	cp ./bin/${BINARY_NAME} /usr/bin/${BINARY_NAME}
	sudo systemctl start cri-shim
