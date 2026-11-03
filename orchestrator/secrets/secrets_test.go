package secrets

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// A made-up value, deliberately not shaped like a real key (secret scanners would flag one).
const fakeKey = "test-value-not-a-real-key-0000000000000000"

type fakeSSM struct {
	value string
	err   error
	calls atomic.Int32
	last  *ssm.GetParameterInput
	mu    sync.Mutex
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.last = in
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: aws.String(f.value)}}, nil
}

func TestGetDecryptsTheNamedParameter(t *testing.T) {
	f := &fakeSSM{value: fakeKey}
	got, err := (&SSM{API: f}).Get(context.Background(), "/cferrante-dev/groq-api-key")
	if err != nil || got != fakeKey {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if aws.ToString(f.last.Name) != "/cferrante-dev/groq-api-key" || !aws.ToBool(f.last.WithDecryption) {
		t.Errorf("request = %+v, want the name with decryption", f.last)
	}
}

func TestGetReadsSSMOncePerParameter(t *testing.T) {
	f := &fakeSSM{value: fakeKey}
	s := &SSM{API: f}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := s.Get(context.Background(), "/p"); err != nil || v != fakeKey {
				t.Errorf("Get = %q, %v", v, err)
			}
		}()
	}
	wg.Wait()
	if n := f.calls.Load(); n != 1 {
		t.Errorf("SSM called %d times for one parameter, want 1", n)
	}
}

func TestFailuresAreNotCachedAndNameTheParameter(t *testing.T) {
	f := &fakeSSM{err: errors.New("ExpiredTokenException: the SSO session has expired")}
	s := &SSM{API: f}
	_, err := s.Get(context.Background(), "/cferrante-dev/groq-api-key")
	if err == nil || !strings.Contains(err.Error(), "/cferrante-dev/groq-api-key") || !strings.Contains(err.Error(), "ExpiredToken") {
		t.Fatalf("err = %v, want the parameter and the cause", err)
	}
	// After logging in again, the next Get succeeds: a failure isn't remembered.
	f.err, f.value = nil, fakeKey
	if v, err := s.Get(context.Background(), "/cferrante-dev/groq-api-key"); err != nil || v != fakeKey {
		t.Errorf("retry = %q, %v", v, err)
	}
}

func TestEmptyValueIsAnError(t *testing.T) {
	if _, err := (&SSM{API: &fakeSSM{value: ""}}).Get(context.Background(), "/p"); err == nil {
		t.Error("want an error for an empty parameter")
	}
}

// Whatever goes wrong, the value must not end up in an error (errors get
// logged and shown).
func TestTheValueNeverAppearsInAnError(t *testing.T) {
	cases := []*fakeSSM{
		{err: errors.New("AccessDenied")},
		{value: "   "},
	}
	for _, f := range cases {
		_, err := (&SSM{API: f}).Get(context.Background(), "/p")
		if err != nil && strings.Contains(err.Error(), fakeKey) {
			t.Errorf("error contains the value: %v", err)
		}
	}
}
