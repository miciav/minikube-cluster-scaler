.PHONY: test shell-test tui-test race cover vet lint tidy-check vuln proto-check build generate check start provider deploy pressure watch remove-pressure cleanup

test: shell-test tui-test
	go test ./...

shell-test:
	./scripts/00-check-prereqs_test.sh
	./scripts/01-start-minikube_test.sh
	./scripts/02-run-provider_test.sh
	./scripts/03-deploy-cluster-autoscaler_test.sh
	./scripts/04-create-pressure_test.sh
	./scripts/06-remove-pressure_test.sh
	./scripts/99-cleanup_test.sh
	./scripts/e2e-scale-down_test.sh
	./deploy/cluster-autoscaler-rbac_test.sh

PY_COVERAGE = uv run --no-project --with 'rich>=14,<15' --with 'coverage>=7,<8' python -m coverage

tui-test:
	$(PY_COVERAGE) run --include=scripts/05-watch-demo.py scripts/05-watch-demo_test.py
	$(PY_COVERAGE) report --show-missing --fail-under=90
	$(PY_COVERAGE) xml -q

race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./cmd/... ./pkg/...
	go tool cover -func=coverage.out | awk '/^total:/ { sub(/%/, "", $$3); print "Go coverage " $$3 "% (minimum 90%)"; exit ($$3 + 0 < 90) }'

vet:
	go vet ./...

lint:
	uvx pre-commit run --all-files

tidy-check:
	go mod tidy -diff
	go mod verify

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

proto-check: generate
	git diff --exit-code proto/

build:
	go build -o /tmp/minikube-externalgrpc-provider ./cmd/provider

generate:
	./proto/generate.sh

check:
	./scripts/00-check-prereqs.sh

start:
	./scripts/01-start-minikube.sh

provider:
	./scripts/02-run-provider.sh

deploy:
	./scripts/03-deploy-cluster-autoscaler.sh

pressure:
	./scripts/04-create-pressure.sh

watch:
	uv run --script scripts/05-watch-demo.py

remove-pressure:
	./scripts/06-remove-pressure.sh

cleanup:
	./scripts/99-cleanup.sh
