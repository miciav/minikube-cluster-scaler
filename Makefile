.PHONY: test race vet build generate check start provider deploy pressure watch cleanup

test:
	go test ./...

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
	./scripts/05-watch-demo.sh

cleanup:
	./scripts/99-cleanup.sh
