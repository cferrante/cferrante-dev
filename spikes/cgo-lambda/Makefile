# Builds the spike's Lambda binary in different ways and checks whether each
# will load on Lambda (provided.al2023, glibc 2.34). Run from this directory:
#   make check-naive   make check-static-libs   make check-full-static
OUT := build
export CGO_ENABLED := 1
export GOOS := linux
export GOARCH := amd64

.PHONY: naive static-libs full-static self-test clean

# Default go build: links glibc and the C++ runtime dynamically.
naive:
	go build -trimpath -o $(OUT)/$@/bootstrap ./cmd/bootstrap

# C++ runtime and libgcc linked in; glibc still dynamic.
static-libs:
	go build -trimpath -ldflags '-extldflags "-static-libstdc++ -static-libgcc"' \
		-o $(OUT)/$@/bootstrap ./cmd/bootstrap

# Everything linked in, glibc included. netgo/osusergo use Go's own DNS and
# user lookup instead of glibc's, which don't work from a static binary.
full-static:
	go build -trimpath -tags netgo,osusergo -ldflags '-extldflags "-static"' \
		-o $(OUT)/$@/bootstrap ./cmd/bootstrap

check-%: %
	./check-glibc.sh $(OUT)/$*/bootstrap

self-test:
	./check-glibc_test.sh

clean:
	rm -rf $(OUT)
