PACKAGE_NAME          := github.com/findyourpaths/goskyr
GOLANG_CROSS_VERSION  ?= v1.22

.PHONY: release-dry-run
release-dry-run:
	@docker run \
		--rm \
		-e CGO_ENABLED=1 \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v `pwd`:/go/src/$(PACKAGE_NAME) \
		-v `pwd`/sysroot:/sysroot \
		-w /go/src/$(PACKAGE_NAME) \
		ghcr.io/goreleaser/goreleaser-cross:${GOLANG_CROSS_VERSION} \
		--clean --skip-validate --skip-publish

.PHONY: release
release:
	@if [ ! -f ".release-env" ]; then \
		echo "\033[91m.release-env is required for release\033[0m";\
		exit 1;\
	fi
	docker run \
		--rm \
		-e CGO_ENABLED=1 \
		--env-file .release-env \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v `pwd`:/go/src/$(PACKAGE_NAME) \
		-v `pwd`/sysroot:/sysroot \
		-w /go/src/$(PACKAGE_NAME) \
		ghcr.io/goreleaser/goreleaser-cross:${GOLANG_CROSS_VERSION} \
		release --clean

.PHONY: update-tests
update-tests:
	@echo "Updating test expectations from actual outputs..."
	@for category in scraping regression; do \
		echo "Processing $$category tests..."; \
		for actual in /tmp/goskyr/main/$$category/*_configs/*.actual.yml; do \
			if [ -f "$$actual" ]; then \
				basename=$$(basename "$$actual" .actual.yml); \
				config_dir=$$(basename $$(dirname "$$actual")); \
				target="testdata/$$category/$$config_dir/$$basename.yml"; \
				echo "Copying $$actual -> $$target"; \
				cp "$$actual" "$$target"; \
			fi; \
		done; \
		for actual in /tmp/goskyr/main/$$category/*_configs/*.actual.json; do \
			if [ -f "$$actual" ]; then \
				basename=$$(basename "$$actual" .actual.json); \
				config_dir=$$(basename $$(dirname "$$actual")); \
				target="testdata/$$category/$$config_dir/$$basename.json"; \
				echo "Copying $$actual -> $$target"; \
				cp "$$actual" "$$target"; \
			fi; \
		done; \
	done
	@echo "Test expectations updated."
