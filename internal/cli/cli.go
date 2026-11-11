// Package cli wires flags, files, the runner and terminal output together.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"httpcli/internal/httpfile"
	"httpcli/internal/run"
	"httpcli/internal/vars"
)

const usage = `httpcli sends the requests of a .http file.

Usage:
  httpcli run <file.http> [flags]

Flags:
  -all               run every request in the file
  -name <name>       run the request with this # @name
  -line <number>     run the request at or before this line
  -env <name>        environment from http-client.env.json
  -var <name=value>  define a variable (repeatable)
  -output <format>   short (default), body, headers or none
  -timeout <dur>     timeout per request (default 30s)

Only the first request is sent unless a flag selects more. Variables are read
from .env, http-client.env.json and the process environment, then -var.
`

// Main runs the command line and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, usage) }

	var (
		all     bool
		name    string
		line    int
		env     string
		output  string
		timeout time.Duration
		cliVars varFlag
	)
	fs.BoolVar(&all, "all", false, "run every request")
	fs.StringVar(&name, "name", "", "request name")
	fs.IntVar(&line, "line", 0, "request line")
	fs.StringVar(&env, "env", "", "environment name")
	fs.StringVar(&output, "output", "short", "output format")
	fs.DurationVar(&timeout, "timeout", 30*time.Second, "request timeout")
	fs.Var(&cliVars, "var", "variable assignment")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	selected := 0
	for _, set := range []bool{all, name != "", line > 0} {
		if set {
			selected++
		}
	}
	if selected > 1 {
		return fail(stderr, "-all, -name and -line are mutually exclusive")
	}
	switch output {
	case "short", "body", "headers", "none":
	default:
		return fail(stderr, "unknown output format %q (want short, body, headers or none)", output)
	}

	path := fs.Arg(0)
	dir := filepath.Dir(path)
	scope, err := baseScope(dir, env, cliVars)
	if err != nil {
		return fail(stderr, "%v", err)
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	file, err := httpfile.Parse(string(src))
	if err != nil {
		return fail(stderr, "%s: %v", path, err)
	}
	plan, err := run.Plan(file, run.Selection{All: all, Name: name, Line: line})
	if err != nil {
		return fail(stderr, "%v", err)
	}

	runner := run.New(&http.Client{Timeout: timeout}, scope, dir)
	failed, setupFailed, printed := false, false, false
	runner.RunAll(context.Background(), file, plan, func(res run.Result) {
		if printed && output != "none" {
			_, _ = fmt.Fprintln(stdout)
		}
		printed = true
		if res.Request == nil {
			setupFailed = true
		}
		failed = failed || res.Failed()
		printResult(stdout, stderr, res, output)
	})
	if setupFailed {
		return 2
	}
	if failed {
		return 1
	}
	return 0
}

func baseScope(dir, env string, cliVars []string) (*vars.Scope, error) {
	dotenv, err := vars.LoadDotEnv(filepath.Join(dir, ".env"))
	if err != nil {
		return nil, err
	}
	envVars, err := vars.LoadEnvFile(filepath.Join(dir, "http-client.env.json"), env)
	if err != nil {
		return nil, err
	}

	scope := vars.New()
	scope.SetStrings(dotenv)
	scope.SetStrings(envVars)
	scope.SetStrings(processEnv())
	for _, assignment := range cliVars {
		name, value, ok := strings.Cut(assignment, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid -var %q, want name=value", assignment)
		}
		scope.Set(name, value)
	}
	return scope, nil
}

// processEnv exposes the shell environment as variables, after the env files
// so that HTTPBIN_BASE_URL=... can retarget a run without editing .env.
func processEnv() map[string]string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		if name, value, ok := strings.Cut(entry, "="); ok {
			values[name] = value
		}
	}
	return values
}

func printResult(stdout, stderr io.Writer, res run.Result, format string) {
	if res.Request == nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", res.Err)
		return
	}
	if format != "none" && res.Request.Title != "" {
		_, _ = fmt.Fprintf(stdout, "### %s\n", res.Request.Title)
	}
	if res.Err != nil {
		_, _ = fmt.Fprintf(stderr, "%s %s\nerror: %v\n", res.Request.Method, res.URL, res.Err)
	} else if format != "none" {
		switch format {
		case "short":
			printStatus(stdout, res)
			writeBody(stdout, res.Response.Body)
		case "body":
			writeBody(stdout, res.Response.Body)
		case "headers":
			printStatus(stdout, res)
			printHeaders(stdout, res.Response)
		}
	}
	for _, failure := range res.Failures {
		_, _ = fmt.Fprintf(stderr, "assertion failed: %s\n", failure)
	}
}

func printStatus(w io.Writer, res run.Result) {
	_, _ = fmt.Fprintf(w, "%s %s\n%s\n", res.Request.Method, res.URL, summary(res.Response))
}

func summary(resp *run.Response) string {
	status := fmt.Sprintf("%d", resp.Status)
	if text := http.StatusText(resp.Status); text != "" {
		status += " " + text
	}
	return fmt.Sprintf("%s (%s, %s)", status, resp.Duration.Round(time.Millisecond), size(len(resp.Body)))
}

func printHeaders(w io.Writer, resp *run.Response) {
	names := make([]string, 0, len(resp.Header))
	for name := range resp.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "%s: %s\n", name, strings.Join(resp.Header.Values(name), ", "))
	}
}

func writeBody(w io.Writer, body []byte) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return
	}
	if json.Valid(body) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err == nil {
			body = pretty.Bytes()
		}
	}
	_, _ = w.Write(body)
	if !bytes.HasSuffix(body, []byte("\n")) {
		_, _ = fmt.Fprintln(w)
	}
}

func size(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f kB", float64(n)/1000)
}

func fail(w io.Writer, format string, args ...any) int {
	_, _ = fmt.Fprintf(w, format+"\n", args...)
	return 2
}

type varFlag []string

func (v *varFlag) String() string { return strings.Join(*v, ",") }

func (v *varFlag) Set(value string) error {
	*v = append(*v, value)
	return nil
}
