// Package config reads settings from environment variables (design §11,
// 12-factor). Commands use the values as flag defaults, so precedence is
// flag > environment > built-in default. An invalid value is an error, never
// a silent fallback to the default.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Getenv looks a variable up; os.Getenv in production, a map in tests.
type Getenv func(string) string

// Env collects settings and the first invalid one.
type Env struct {
	get Getenv
	err error
}

// New reads from get.
func New(get Getenv) *Env { return &Env{get: get} }

// String returns the variable, or def when it is unset or empty.
func (e *Env) String(name, def string) string {
	if v := e.get(name); v != "" {
		return v
	}
	return def
}

// Int returns the variable as an integer, or def when unset.
func (e *Env) Int(name string, def int) int {
	v := e.get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.fail(fmt.Errorf("%s=%q: want a whole number", name, v))
		return def
	}
	return n
}

// Bool returns the variable as a boolean (true/false, 1/0), or def when unset.
func (e *Env) Bool(name string, def bool) bool {
	v := e.get(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.fail(fmt.Errorf("%s=%q: want true or false", name, v))
		return def
	}
	return b
}

// Err is the first invalid variable, if any.
func (e *Env) Err() error { return e.err }

func (e *Env) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// WithDotEnv returns a Getenv that consults get first and falls back to the
// KEY=VALUE lines of the file at path, so variables already set in the shell
// win over the file. A missing file is not an error.
//
// Lines are KEY=VALUE, optionally prefixed with "export "; blank lines and
// lines starting with # are skipped; matching single or double quotes around
// a value are removed. Nothing else is interpreted, so a # inside a value is
// part of it. The process environment is never modified.
func WithDotEnv(path string, get Getenv) (Getenv, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return get, nil
	}
	if err != nil {
		return nil, err
	}
	file := map[string]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("%s line %d: want KEY=VALUE", path, i+1)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		file[key] = value
	}
	return func(k string) string {
		if v := get(k); v != "" {
			return v
		}
		return file[k]
	}, nil
}
