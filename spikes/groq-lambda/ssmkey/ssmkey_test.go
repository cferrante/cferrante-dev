package ssmkey

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	in    *ssm.GetParameterInput
	value *string
	err   error
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: f.value}}, nil
}

func TestLoadDecryptsNamedParameter(t *testing.T) {
	f := &fakeSSM{value: aws.String("gsk_test")}
	got, err := Load(context.Background(), f, "/p/key")
	if err != nil || got != "gsk_test" {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if f.in == nil || aws.ToString(f.in.Name) != "/p/key" || !aws.ToBool(f.in.WithDecryption) {
		t.Errorf("request = %+v, want name /p/key with decryption", f.in)
	}
}

func TestLoadRejectsEmptyValue(t *testing.T) {
	if _, err := Load(context.Background(), &fakeSSM{value: aws.String("")}, "/p/key"); err == nil {
		t.Error("want an error for an empty value")
	}
}

func TestLoadNamesParameterOnError(t *testing.T) {
	_, err := Load(context.Background(), &fakeSSM{err: errors.New("AccessDenied")}, "/p/key")
	if err == nil || !strings.Contains(err.Error(), "/p/key") || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err = %v, want it to name the parameter and the cause", err)
	}
}
