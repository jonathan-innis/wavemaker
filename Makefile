AWS_ACCOUNT_ID ?= $(shell aws sts get-caller-identity --query Account --output text)
KO_DOCKER_REPO ?= ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_DEFAULT_REGION}.amazonaws.com/dev/wavemaker
SYSTEM_NAMESPACE ?= "wavemaker"

image: ## Build the Karpenter controller images using ko build
	$(eval CONTROLLER_IMG=$(shell $(WITH_GOFLAGS) KO_DOCKER_REPO="$(KO_DOCKER_REPO)" ko build --bare github.com/jonathan-innis/tools/wavemaker))
	$(eval IMG_REPOSITORY=$(shell echo $(CONTROLLER_IMG) | cut -d "@" -f 1 | cut -d ":" -f 1))
	$(eval IMG_TAG=$(shell echo $(CONTROLLER_IMG) | cut -d "@" -f 1 | cut -d ":" -f 2 -s))
	$(eval IMG_DIGEST=$(shell echo $(CONTROLLER_IMG) | cut -d "@" -f 2))

apply: image ## Deploy the controller from the current state of your git repository into your ~/.kube/config cluster
	helm upgrade --install wavemaker charts/wavemaker --namespace ${SYSTEM_NAMESPACE} \
		--set controller.image.repository=$(IMG_REPOSITORY) \
		--set controller.image.tag=$(IMG_TAG) \
		--set controller.image.digest=$(IMG_DIGEST) \
		--create-namespace