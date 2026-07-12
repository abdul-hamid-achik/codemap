.PHONY: test lint fmt

test:
	./test/test.sh

# Best-effort local lint: uses shellcheck/yamllint if installed, skips
# quietly otherwise (CI, .github/workflows/ci.yml, always runs both).
lint:
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck scripts/*.sh test/test.sh; \
	else \
		echo "shellcheck not installed locally — skipping (CI runs it)"; \
	fi
	@if command -v yamllint >/dev/null 2>&1; then \
		yamllint action.yml gitlab/codemap-review.yml examples/dogfood-workflow.yml .github/workflows/*.yml; \
	else \
		echo "yamllint not installed locally — skipping (CI runs it)"; \
	fi
