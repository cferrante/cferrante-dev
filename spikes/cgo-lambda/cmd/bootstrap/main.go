// Command bootstrap is a minimal Lambda custom runtime for provided.al2023.
// Each event's numbers are summed by the C++ engine via cgo. It talks to the
// Lambda Runtime API over HTTP directly, so the spike needs no dependencies.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/cferrante/cferrante-dev/spikes/cgo-lambda/engine"
)

type event struct {
	Numbers []int64 `json:"numbers"`
}

type response struct {
	Sum  int64  `json:"sum"`
	Libc string `json:"libc"` // glibc in use at run time
	OS   string `json:"os"`   // proves where it ran
	Arch string `json:"arch"`
	Go   string `json:"go"`
}

func handle(body []byte) ([]byte, error) {
	var ev event
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("invalid event: %w", err)
	}
	return json.Marshal(response{
		Sum:  engine.Sum(ev.Numbers),
		Libc: engine.LibcVersion(),
		OS:   osName(),
		Arch: runtime.GOARCH,
		Go:   runtime.Version(),
	})
}

// osName returns PRETTY_NAME from /etc/os-release, or "unknown".
func osName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return "unknown"
}

func main() {
	base := "http://" + os.Getenv("AWS_LAMBDA_RUNTIME_API") + "/2018-06-01/runtime/invocation/"
	for {
		res, err := http.Get(base + "next")
		if err != nil {
			log.Fatalf("get next invocation: %v", err)
		}
		id := res.Header.Get("Lambda-Runtime-Aws-Request-Id")
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			log.Fatalf("read invocation: %v", err)
		}

		out, err := handle(body)
		path := id + "/response"
		if err != nil {
			path = id + "/error"
			out, _ = json.Marshal(map[string]string{"errorMessage": err.Error(), "errorType": "InvalidEvent"})
		}
		post, err := http.Post(base+path, "application/json", bytes.NewReader(out))
		if err != nil {
			log.Fatalf("post result: %v", err)
		}
		post.Body.Close()
	}
}
