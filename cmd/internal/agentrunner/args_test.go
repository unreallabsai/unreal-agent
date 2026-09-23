package agentrunner

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/unreallabsai/unreal-agent/harness/llm"
)

func TestRunMainRequestSources(t *testing.T) {
	for _, test := range []struct {
		name  string
		args  []string
		stdin string
		want  []string
	}{
		{name: "stdin", stdin: `{"prompt":"from stdin"}`, want: []string{"from stdin"}},
		{name: "positional", args: []string{`{"prompt":"from argument"}`}, want: []string{"from argument"}},
		{name: "positional messages", args: []string{`{"messages":[{"content":"first"},{"content":"second"}]}`}, want: []string{"first", "second"}},
		{name: "positional after separator", args: []string{"--", `{"prompt":"after separator"}`}, want: []string{"after separator"}},
		{name: "prompt", args: []string{"-p", "plain prompt"}, want: []string{"plain prompt"}},
		{name: "prompt equals", args: []string{"-p=plain prompt"}, want: []string{"plain prompt"}},
		{name: "empty prompt on stdin", stdin: `{"prompt":""}`, want: []string{""}},
		{name: "empty positional prompt", args: []string{`{"prompt":""}`}, want: []string{""}},
		{name: "empty prompt flag", args: []string{"-p", ""}, want: []string{""}},
		{name: "quoted multiline prompt", args: []string{"-p", "  say \"hello\"\nC:\\work\t世界  "}, want: []string{"  say \"hello\"\nC:\\work\t世界  "}},
		{name: "JSON text as prompt", args: []string{"-p", `{"messages":[]}`}, want: []string{`{"messages":[]}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			client := &fakeClient{respond: func(_ context.Context, request llm.Request) (llm.Response, error) {
				called = true
				var messages []string
				for _, item := range request.Input {
					if item.Type == llm.ItemMessage {
						message := item.Data.(llm.Message)
						if message.Role == llm.RoleUser {
							messages = append(messages, message.Text)
						}
					}
				}
				if !slices.Equal(messages, test.want) {
					return llm.Response{}, fmt.Errorf("user messages = %q, want %q", messages, test.want)
				}
				return llm.Response{ID: "done", Stop: llm.StopComplete}, nil
			}}
			input := iotest.ErrReader(errors.New("stdin must not be read"))
			if test.stdin != "" {
				input = strings.NewReader(test.stdin)
			}
			args := append([]string{"-workspace", t.TempDir(), "-session-directory", t.TempDir()}, test.args...)
			var stdout, stderr bytes.Buffer
			code := RunMain(t.Context(), args, func(name string) string {
				if name == llmAPIKeyEnvironment {
					return "secret"
				}
				return ""
			}, func() []string { return nil }, input, &stdout, &stderr, testConfig(client))
			if code != 0 || !called || !client.closed {
				t.Fatalf("exit = %d, called = %v, closed = %v, stderr = %s", code, called, client.closed, stderr.String())
			}
		})
	}
}

func TestRunMainRejectsInvalidRequestArguments(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing prompt", args: []string{"-p"}, want: "flag needs an argument: -p"},
		{name: "conflicting inputs", args: []string{"-p", "hello", `{"prompt":"world"}`}, want: "-p cannot be combined"},
		{name: "empty prompt conflicts", args: []string{"-p=", `{"prompt":"world"}`}, want: "-p cannot be combined"},
		{name: "extra arguments", args: []string{`{"prompt":"hello"}`, `{"prompt":"world"}`}, want: "at most one positional JSON request"},
		{name: "invalid JSON", args: []string{"plain text"}, want: "invalid JSON"},
		{name: "unknown field", args: []string{`{"prompt":"hello","unknown":true}`}, want: "unknown object member"},
		{name: "empty argument", args: []string{""}, want: "empty input"},
		{name: "invalid request", args: []string{`{"messages":[]}`}, want: "messages must not be empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := RunMain(t.Context(), test.args, func(string) string {
				t.Fatal("invalid arguments reached environment setup")
				return ""
			}, func() []string { return nil }, iotest.ErrReader(errors.New("stdin must not be read")), &stdout, &stderr,
				Config{Name: "test-runner", ParseRequest: parseTestRequest})
			if code != 1 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit = %d, stderr = %s, want %q", code, stderr.String(), test.want)
			}
			var event errorEvent
			if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "error" || !strings.Contains(event.Message, test.want) {
				t.Fatalf("error event = %#v, want %q", event, test.want)
			}
		})
	}
}

func TestRunMainUsageWriteFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown flag", args: []string{"-unknown"}, want: "flag provided but not defined: -unknown"},
		{name: "missing prompt", args: []string{"-p"}, want: "flag needs an argument: -p"},
		{name: "invalid duration", args: []string{"-tool-heartbeat-interval=invalid"}, want: "invalid value"},
		{name: "help", args: []string{"-h"}, want: "write usage"},
		{name: "long help", args: []string{"-help"}, want: "write usage"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, stderr := io.Pipe()
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			defer stderr.Close()
			var stdout bytes.Buffer
			code := RunMain(t.Context(), test.args, func(string) string {
				t.Fatal("invalid arguments reached environment setup")
				return ""
			}, func() []string { return nil }, iotest.ErrReader(errors.New("stdin must not be read")), &stdout, stderr,
				Config{Name: "test-runner", ParseRequest: parseTestRequest})
			if code != 1 {
				t.Fatalf("exit = %d, stdout = %q", code, stdout.String())
			}
			var event errorEvent
			if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "error" || !strings.Contains(event.Message, test.want) ||
				!strings.Contains(event.Message, "write usage: "+io.ErrClosedPipe.Error()) {
				t.Fatalf("error event = %#v, want %q and usage write error", event, test.want)
			}
		})
	}
}

func TestRunMainHelp(t *testing.T) {
	for _, arg := range []string{"-h", "-help"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := RunMain(t.Context(), []string{arg}, func(string) string {
				t.Fatal("help reached environment setup")
				return ""
			}, func() []string { return nil }, iotest.ErrReader(errors.New("stdin must not be read")), &stdout, &stderr,
				Config{Name: "test-runner", ParseRequest: func(io.Reader) (Request, ToolFactory, error) {
					t.Fatal("help reached request parsing")
					return Request{}, nil, nil
				}})
			if code != 0 || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			for _, want := range []string{
				"test-runner [options] < request.json", "test-runner [options] 'JSON request'", "test-runner [options] -p 'prompt'",
				"-p prompt", "-workspace", "-session-directory", "-log-directory", "-tool-heartbeat-interval",
				"$XDG_STATE_HOME/unreal-agent/sessions", "$HOME/.local/state/unreal-agent/sessions",
				"optional session JSONL log directory; unset writes only to stdout",
				"Request schema", "messages:", "role:", "content:", "message_id?:", "prompt:", "model:", "max_attempts:",
				"system_prompt:", "thinking_level:", "session_id:", "disallowed_tools:", "extra_allowed_tools:", "include_partial_messages:",
			} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("help missing %q: %s", want, stderr.String())
				}
			}
			if strings.Contains(stderr.String(), "mcp_servers") {
				t.Fatal("common request schema includes MCP servers")
			}
		})
	}
}
