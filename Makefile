.PHONY: test shell-test tui-test race vet build generate check start provider deploy pressure watch remove-pressure cleanup

test: shell-test tui-test
	go test ./...

shell-test:
	./scripts/01-start-minikube_test.sh
	./scripts/03-deploy-cluster-autoscaler_test.sh
	./scripts/04-create-pressure_test.sh
	./scripts/06-remove-pressure_test.sh
	./scripts/e2e-scale-down_test.sh
	./deploy/cluster-autoscaler-rbac_test.sh

tui-test:
	uv run --script scripts/05-watch-demo_test.py

race:
	go test -race ./...

vet:
	go vet ./...

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
