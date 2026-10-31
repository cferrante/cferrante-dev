// Command bootstrap streams Server-Sent Events from a Lambda Function URL in
// RESPONSE_STREAM mode. Spike 1.3 checks that they reach the client one by
// one rather than all at the end.
package main

import (
	"context"
	"io"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/cferrante/cferrante-dev/spikes/sse-lambda/sse"
)

// Variables so the test can make the stream short and fast.
var (
	count    = 10
	interval = 200 * time.Millisecond
)

// handler returns at once; the body is a pipe that the SSE writer fills in
// the background while aws-lambda-go streams it to the caller.
func handler(ctx context.Context, _ *events.LambdaFunctionURLRequest) (*events.LambdaFunctionURLStreamingResponse, error) {
	r, w := io.Pipe()
	go func() { w.CloseWithError(sse.Stream(ctx, w, count, interval)) }()
	return &events.LambdaFunctionURLStreamingResponse{
		StatusCode: 200,
		Headers: map[string]string{
			"Content-Type":  "text/event-stream",
			"Cache-Control": "no-cache",
		},
		Body: r,
	}, nil
}

func main() {
	lambda.Start(handler)
}
