package config

import (
	"os"
	"strings"
	"testing"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func TestValuesFromTheEnvironmentOrDefaults(t *testing.T) {
	e := New(env(map[string]string{"ADDR": "0.0.0.0:9000", "DIM": "512", "ON": "true", "OFF": "0"}))
	if got := e.String("ADDR", "x"); got != "0.0.0.0:9000" {
		t.Errorf("String = %q", got)
	}
	if got := e.String("UNSET", "fallback"); got != "fallback" {
		t.Errorf("unset String = %q", got)
	}
	if got := e.Int("DIM", 1); got != 512 {
		t.Errorf("Int = %d", got)
	}
	if got := e.Int("UNSET", 7); got != 7 {
		t.Errorf("unset Int = %d", got)
	}
	if !e.Bool("ON", false) || e.Bool("OFF", true) || !e.Bool("UNSET", true) {
		t.Error("Bool values wrong")
	}
	if e.Err() != nil {
		t.Errorf("Err = %v", e.Err())
	}
}

func TestInvalidValuesAreErrorsNamingTheVariable(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"int":  {"DIM": "abc"},
		"bool": {"ON": "maybe"},
	} {
		e := New(env(m))
		e.Int("DIM", 1)
		e.Bool("ON", false)
		if err := e.Err(); err == nil || !strings.Contains(err.Error(), map[string]string{"int": "DIM", "bool": "ON"}[name]) {
			t.Errorf("%s: Err = %v, want an error naming the variable", name, err)
		}
	}
}

func TestEmptyMeansUnset(t *testing.T) {
	e := New(env(map[string]string{"ADDR": "", "DIM": ""}))
	if e.String("ADDR", "d") != "d" || e.Int("DIM", 3) != 3 || e.Err() != nil {
		t.Error("an empty variable should count as unset")
	}
}

func TestDotEnvFallsBackBehindTheShell(t *testing.T) {
	path := t.TempDir() + "/.env"
	content := "# local settings\n\nCHAT_MODEL=llama3.1\nexport LISTEN_ADDR=\"127.0.0.1:9000\"\nEMBED_DIM='512'\nEMPTY=\nURL=http://h:1/x?a=b # not a comment inside a value\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := env(map[string]string{"CHAT_MODEL": "qwen3:4b"})
	get, err := WithDotEnv(path, shell)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"CHAT_MODEL":  "qwen3:4b", // the shell wins
		"LISTEN_ADDR": "127.0.0.1:9000",
		"EMBED_DIM":   "512",
		"EMPTY":       "",
		"URL":         "http://h:1/x?a=b # not a comment inside a value",
		"UNSET":       "",
	} {
		if got := get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestMissingDotEnvIsFine(t *testing.T) {
	get, err := WithDotEnv(t.TempDir()+"/absent", env(map[string]string{"A": "1"}))
	if err != nil || get("A") != "1" {
		t.Errorf("get(A) = %q, err %v", get("A"), err)
	}
}

func TestMalformedDotEnvNamesTheLine(t *testing.T) {
	path := t.TempDir() + "/.env"
	os.WriteFile(path, []byte("A=1\nthis is not a setting\n"), 0o600)
	if _, err := WithDotEnv(path, env(nil)); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("err = %v, want it to name line 2", err)
	}
}
