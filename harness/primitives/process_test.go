package primitives_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/unreallabsai/unreal-agent/harness/primitives"
)

func startProcessWithAllPipes(ctx context.Context, request primitives.ProcessStartRequest) *processInvocation {
	request.Pipes = primitives.ProcessPipeAll
	return startProcess(ctx, request)
}

func TestProcessCapturesOutputIntoPaths(t *testing.T) {
	directory := t.TempDir()
	stdoutPath := filepath.Join(directory, "stdout")
	stderrPath := filepath.Join(directory, "stderr")
	for _, path := range []string{stdoutPath, stderrPath} {
		if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	request := primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"printf captured-out; printf captured-err >&2; exit 7",
		},
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	}
	events := collectEvents(startProcess(t.Context(), request).Events())
	if len(events) != 2 ||
		events[0].Type != primitives.PrimitiveEventProcessStarted ||
		events[1].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", events)
	}
	if result := eventResult[primitives.ProcessExitResult](t, events[1]); result.ExitCode != 7 || result.Signal != 0 {
		t.Fatalf("exit = %#v", result)
	}
	for path, want := range map[string]string{
		stdoutPath: "captured-out",
		stderrPath: "captured-err",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != want {
			t.Fatalf("contents of %q = %q, want %q", path, contents, want)
		}
	}
}

func TestProcessCancellationCompletesCapturedOutput(t *testing.T) {
	directory := t.TempDir()
	stdoutPath := filepath.Join(directory, "stdout")
	stderrPath := filepath.Join(directory, "stderr")
	for _, path := range []string{stdoutPath, stderrPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcess(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"trap 'printf final; printf error >&2; exit 0' TERM; printf ready; while :; do :; done",
		},
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	waitForFileContent(t, stdoutPath, "ready")

	cancel()
	singleEvent(t, collectEvents(events), primitives.PrimitiveEventCanceled)
	for path, want := range map[string]string{
		stdoutPath: "readyfinal",
		stderrPath: "error",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != want {
			t.Fatalf("contents of %q = %q, want %q", path, contents, want)
		}
	}
}

func TestProcessRejectsInvalidCaptureConfiguration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "capture")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, request := range []primitives.ProcessStartRequest{
		{Path: "/bin/true", StdoutPath: "relative"},
		{Path: "/bin/true", StderrPath: "relative"},
		{Path: "/bin/true", Pipes: primitives.ProcessPipeStdout, StdoutPath: path},
		{Path: "/bin/true", Pipes: primitives.ProcessPipeStderr, StderrPath: path},
		{Path: "/bin/true", StdoutPath: path, StderrPath: path},
	} {
		event := singleEvent(
			t,
			collectEvents(startProcess(t.Context(), request).Events()),
			primitives.PrimitiveEventFailed,
		)
		if failure := eventResult[primitives.PrimitiveFailureResult](t, event); failure.Error == "" {
			t.Fatalf("failure = %#v", failure)
		}
	}
}

func TestProcessRejectsAliasedCapturePathsBeforeTruncating(t *testing.T) {
	directory := t.TempDir()
	stdoutPath := filepath.Join(directory, "stdout")
	stderrPath := filepath.Join(directory, "stderr")
	want := []byte("preserved")
	if err := os.WriteFile(stdoutPath, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(stdoutPath, stderrPath); err != nil {
		t.Fatal(err)
	}

	event := singleEvent(
		t,
		collectEvents(startProcess(t.Context(), primitives.ProcessStartRequest{
			Path:       "/bin/true",
			StdoutPath: stdoutPath,
			StderrPath: stderrPath,
		}).Events()),
		primitives.PrimitiveEventFailed,
	)
	failure := eventResult[primitives.PrimitiveFailureResult](t, event)
	if !strings.Contains(failure.Error, "same file") {
		t.Fatalf("failure = %q, want aliased-capture failure", failure.Error)
	}
	contents, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(want) {
		t.Fatalf("contents = %q, want %q", contents, want)
	}
}

func TestProcessRejectsInvalidCapturePath(t *testing.T) {
	events := collectEvents(startProcess(t.Context(), primitives.ProcessStartRequest{
		Path:       "/bin/true",
		StdoutPath: t.TempDir(),
	}).Events())
	event := singleEvent(t, events, primitives.PrimitiveEventFailed)
	if failure := eventResult[primitives.PrimitiveFailureResult](t, event); failure.Error == "" {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestProcessStreamsOutputAndExit(t *testing.T) {
	stdout := strings.Repeat("stdout-", primitives.ProcessOutputChunkSize/4)
	stderr := strings.Repeat("stderr-", primitives.ProcessOutputChunkSize/4)
	request := primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"printf '%s' \"$PROCESS_STDOUT\"; printf '%s' \"$PROCESS_STDERR\" >&2; exit 7",
		},
		Environment: []string{
			"PROCESS_STDOUT=" + stdout,
			"PROCESS_STDERR=" + stderr,
		},
	}
	events := collectEvents(startProcessWithAllPipes(t.Context(), request).Events())
	if len(events) < 4 {
		t.Fatalf("events = %#v", events)
	}
	assertProcessEventIdentity(t, events, request.Source, request.CorrelationID)
	if events[0].Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("first event = %#v", events[0])
	}
	if started := eventResult[primitives.ProcessStartedResult](t, events[0]); started.PID <= 0 {
		t.Fatalf("PID = %d", started.PID)
	}

	var gotStdout []byte
	var gotStderr []byte
	var stdoutOffset int64
	var stderrOffset int64
	for _, event := range events[1 : len(events)-1] {
		if event.Type != primitives.PrimitiveEventProcessOutput {
			t.Fatalf("stream event = %#v", event)
		}
		output := eventResult[primitives.ProcessOutputResult](t, event)
		if len(output.Data) > primitives.ProcessOutputChunkSize {
			t.Fatalf("output chunk = %d bytes", len(output.Data))
		}
		switch output.Stream {
		case primitives.ProcessStdout:
			if output.Offset != stdoutOffset {
				t.Fatalf("stdout offset = %d, want %d", output.Offset, stdoutOffset)
			}
			gotStdout = append(gotStdout, output.Data...)
			stdoutOffset += int64(len(output.Data))
		case primitives.ProcessStderr:
			if output.Offset != stderrOffset {
				t.Fatalf("stderr offset = %d, want %d", output.Offset, stderrOffset)
			}
			gotStderr = append(gotStderr, output.Data...)
			stderrOffset += int64(len(output.Data))
		default:
			t.Fatalf("stream = %d", output.Stream)
		}
	}
	if string(gotStdout) != stdout {
		t.Fatalf("stdout size = %d, want %d", len(gotStdout), len(stdout))
	}
	if string(gotStderr) != stderr {
		t.Fatalf("stderr size = %d, want %d", len(gotStderr), len(stderr))
	}
	last := events[len(events)-1]
	if last.Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("last event = %#v", last)
	}
	if result := eventResult[primitives.ProcessExitResult](t, last); result.ExitCode != 7 || result.Signal != 0 {
		t.Fatalf("exit = %#v", result)
	}
}

func TestProcessInput(t *testing.T) {
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/cat",
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	written := singleEvent(t, collectEvents(process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
		Source:        "operation-1",
		CorrelationID: "write-1",
		Data:          []byte("input data"),
	})), primitives.PrimitiveEventProcessInputWritten)
	if result := eventResult[primitives.ProcessWriteResult](t, written); result.Count != len("input data") {
		t.Fatalf("write = %#v", written)
	}
	closed := singleEvent(t, collectEvents(process.CloseInput(t.Context(), primitives.ProcessCloseInputRequest{
		Source:        "operation-1",
		CorrelationID: "close-1",
	})), primitives.PrimitiveEventProcessInputClosed)
	if closed.Result != nil {
		t.Fatalf("close = %#v", closed)
	}

	rest := collectEvents(events)
	assertProcessEventTypes(t, rest, map[primitives.PrimitiveEventType]int{
		primitives.PrimitiveEventProcessOutput: 1,
		primitives.PrimitiveEventProcessExited: 1,
	})
	if last := rest[len(rest)-1]; last.Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("last event = %#v", last)
	}

	for _, event := range rest {
		if event.Type == primitives.PrimitiveEventProcessOutput {
			output := eventResult[primitives.ProcessOutputResult](t, event)
			if output.Stream != primitives.ProcessStdout || output.Offset != 0 || string(output.Data) != "input data" {
				t.Fatalf("output = %#v", event)
			}
		}
	}
}

func TestFileReadComposesWithProcessInput(t *testing.T) {
	data := []byte(strings.Repeat("composed input\n", 8*1024))
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write input file: %v", err)
	}

	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/cat",
	})
	processEvents := make(chan []primitives.PrimitiveEvent, 1)
	go func() {
		processEvents <- collectEvents(process.Events())
	}()

	readEvents := readFile(t.Context(), primitives.IOReadRequest{
		Source:        "operation-1",
		CorrelationID: "read-1",
		Path:          path,
		Count:         int64(len(data)),
	})
	writes := 0

read:
	for {
		event := <-readEvents
		switch event.Type {
		case primitives.PrimitiveEventIOReadOutput:
			chunk := eventResult[primitives.IOReadOutputResult](t, event)
			correlationID := primitives.CorrelationID("write-" + strconv.Itoa(writes))
			written := singleEvent(t, collectEvents(process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
				Source:        "operation-1",
				CorrelationID: correlationID,
				Data:          chunk.Data,
			})), primitives.PrimitiveEventProcessInputWritten)
			if result := eventResult[primitives.ProcessWriteResult](t, written); result.Count != len(chunk.Data) {
				t.Fatalf("write %d = %#v", writes, result)
			}
			writes++
		case primitives.PrimitiveEventIOReadCompleted:
			if result := eventResult[primitives.IOReadCompletedResult](t, event); result.Size != int64(len(data)) {
				t.Fatalf("read completed = %#v", result)
			}
			break read
		default:
			t.Fatalf("read event = %#v", event)
		}
	}
	if writes < 2 {
		t.Fatalf("writes = %d, want multiple chunks", writes)
	}
	singleEvent(t, collectEvents(process.CloseInput(t.Context(), primitives.ProcessCloseInputRequest{
		Source:        "operation-1",
		CorrelationID: "close-1",
	})), primitives.PrimitiveEventProcessInputClosed)

	var stdout []byte
	var exited bool
	for _, event := range <-processEvents {
		switch event.Type {
		case primitives.PrimitiveEventProcessOutput:
			output := eventResult[primitives.ProcessOutputResult](t, event)
			if output.Stream == primitives.ProcessStdout {
				stdout = append(stdout, output.Data...)
			}
		case primitives.PrimitiveEventProcessExited:
			exited = eventResult[primitives.ProcessExitResult](t, event).ExitCode == 0
		}
	}
	if !exited || string(stdout) != string(data) {
		t.Fatalf("exited = %t, stdout size = %d, want %d", exited, len(stdout), len(data))
	}
}

func TestProcessSignalsRemainResponsiveDuringBlockedInputWrite(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "writer-started")
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"head -c 1 >/dev/null; printf '%d\n' $$ > \"$1\"; exec sleep 30",
			"sh",
			marker,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	writeEvents := process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
		Source:        "operation-1",
		CorrelationID: "write-1",
		Data:          make([]byte, 1024*1024),
	})
	waitForProcessPID(t, marker)
	signaled := singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:        "operation-1",
		CorrelationID: "signal-1",
		Signal:        syscall.SIGTERM,
	})), primitives.PrimitiveEventProcessSignaled)
	if signaled.CorrelationID != "signal-1" {
		t.Fatalf("signaled = %#v", signaled)
	}

	rest := collectProcessEvents(t, events)
	last := rest[len(rest)-1]
	if last.Type != primitives.PrimitiveEventProcessExited ||
		eventResult[primitives.ProcessExitResult](t, last).Signal != syscall.SIGTERM {
		t.Fatalf("last event = %#v", last)
	}
	failedWrite := singleEvent(t, collectEvents(writeEvents), primitives.PrimitiveEventProcessInputWriteFailed)
	if result := eventResult[primitives.ProcessWriteFailureResult](t, failedWrite); result.Count <= 0 || result.Error == "" {
		t.Fatalf("write failure = %#v", result)
	}
}

func TestProcessCloseInterruptsBlockedInputWrite(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "writer-started")
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"head -c 1 >/dev/null; printf '%d\n' $$ > \"$1\"; exec sleep 30",
			"sh",
			marker,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	writeEvents := process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
		Source:        "operation-1",
		CorrelationID: "write-1",
		Data:          make([]byte, 1024*1024),
	})
	waitForProcessPID(t, marker)
	singleEvent(t, collectEvents(process.CloseInput(t.Context(), primitives.ProcessCloseInputRequest{
		Source:        "operation-1",
		CorrelationID: "close-1",
	})), primitives.PrimitiveEventProcessInputClosed)
	failedWrite := singleEvent(t, collectEvents(writeEvents), primitives.PrimitiveEventProcessInputWriteFailed)
	if result := eventResult[primitives.ProcessWriteFailureResult](t, failedWrite); result.Count <= 0 || result.Error == "" {
		t.Fatalf("write failure = %#v", result)
	}
	singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:        "operation-1",
		CorrelationID: "signal-1",
		Signal:        syscall.SIGTERM,
	})), primitives.PrimitiveEventProcessSignaled)
	rest := collectProcessEvents(t, events)
	if last := rest[len(rest)-1]; last.Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("last event = %#v", last)
	}
}

func TestProcessCloseInputEndsInput(t *testing.T) {
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/cat",
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	closed := singleEvent(t, collectEvents(process.CloseInput(t.Context(), primitives.ProcessCloseInputRequest{
		Source:        "operation-1",
		CorrelationID: "close-1",
	})), primitives.PrimitiveEventProcessInputClosed)
	if closed.CorrelationID != "close-1" {
		t.Fatalf("closed = %#v", closed)
	}

	rest := collectEvents(events)
	if len(rest) != 1 || rest[0].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", rest)
	}
	exited := rest[0]
	if result := eventResult[primitives.ProcessExitResult](t, exited); result.ExitCode != 0 {
		t.Fatalf("exit = %#v", result)
	}
}

func TestProcessInputRemainsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "exec sleep 30"},
	})
	if started := receiveProcessEvent(t, process.Events()); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	for _, correlationID := range []primitives.CorrelationID{"close-1", "close-2"} {
		event := singleEvent(t, collectEvents(process.CloseInput(t.Context(), primitives.ProcessCloseInputRequest{
			Source:        "operation-1",
			CorrelationID: correlationID,
		})), primitives.PrimitiveEventProcessInputClosed)
		if event.Source != "operation-1" || event.CorrelationID != correlationID {
			t.Fatalf("close event = %#v", event)
		}
	}

	event := singleEvent(t, collectEvents(process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
		Source:        "operation-1",
		CorrelationID: "write-1",
		Data:          []byte("input after close"),
	})), primitives.PrimitiveEventProcessInputWriteFailed)
	if event.Source != "operation-1" || event.CorrelationID != "write-1" {
		t.Fatalf("write event = %#v", event)
	}
	result := eventResult[primitives.ProcessWriteFailureResult](t, event)
	if result.Count != 0 || result.Error == "" {
		t.Fatalf("write failure = %#v", result)
	}

	cancel()
	_ = collectEvents(process.Events())
}

func TestProcessSignal(t *testing.T) {
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "exec sleep 30"},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:        "operation-1",
		CorrelationID: "signal-1",
		Signal:        syscall.SIGTERM,
	})), primitives.PrimitiveEventProcessSignaled)

	rest := collectEvents(events)
	if len(rest) != 1 || rest[0].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", rest)
	}
	result := eventResult[primitives.ProcessExitResult](t, rest[0])
	if result.ExitCode != -1 || result.Signal != syscall.SIGTERM {
		t.Fatalf("exit = %#v", result)
	}
}

func TestProcessSignalPropagatesToChildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"sleep 30 & printf '%d\\n' $! > \"$1\"; wait",
			"sh",
			marker,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	descendantPID := waitForProcessPID(t, marker)
	descendantGone := cleanupProcessPID(t, descendantPID)
	singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:              "operation-1",
		CorrelationID:       "signal-1",
		Signal:              syscall.SIGTERM,
		PropagateToChildren: true,
	})), primitives.PrimitiveEventProcessSignaled)

	rest := collectEvents(events)
	if len(rest) != 1 || rest[0].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", rest)
	}
	waitForProcessGone(t, descendantPID)
	descendantGone()
}

func TestProcessSignalDoesNotPropagateByDefault(t *testing.T) {
	directory := t.TempDir()
	pidMarker := filepath.Join(directory, "pid")
	aliveMarker := filepath.Join(directory, "alive")
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			`trap ':' TERM; /bin/sh -c "$3" sh "$1" "$2" & while :; do wait; done`,
			"sh",
			pidMarker,
			aliveMarker,
			`trap 'printf "alive\n" > "$2"; exit 0' USR1; printf '%d\n' $$ > "$1"; while :; do :; done`,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	descendantPID := waitForProcessPID(t, pidMarker)
	descendantGone := cleanupProcessPID(t, descendantPID)
	singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:        "operation-1",
		CorrelationID: "signal-1",
		Signal:        syscall.SIGTERM,
	})), primitives.PrimitiveEventProcessSignaled)
	if err := syscall.Kill(descendantPID, syscall.SIGUSR1); err != nil {
		t.Fatalf("signal descendant: %v", err)
	}
	waitForFileContent(t, aliveMarker, "alive\n")
	waitForProcessGone(t, descendantPID)
	descendantGone()

	cancel()
	singleEvent(t, collectEvents(events), primitives.PrimitiveEventCanceled)
}

func TestCancelProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "exec sleep 30"},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	cancel()

	canceled := singleEvent(t, collectEvents(events), primitives.PrimitiveEventCanceled)
	if canceled.Source != "operation-1" || canceled.CorrelationID != "process-1" || canceled.Result != nil {
		t.Fatalf("canceled = %#v", canceled)
	}
}

func TestCompletedProcessIsNotCanceledAfterCancellationStops(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"printf '%d\\n' $$ > \"$1\"; printf output; exit 7",
			"sh",
			marker,
		},
	})
	events := process.Events()
	collectedEvents := make(chan []primitives.PrimitiveEvent, 1)
	go func() {
		collectedEvents <- collectEvents(events)
	}()
	pid := waitForProcessPID(t, marker)
	waitForProcessGone(t, pid)
	cancel()

	collected := <-collectedEvents
	if len(collected) < 2 || collected[0].Type != primitives.PrimitiveEventProcessStarted ||
		collected[len(collected)-1].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", collected)
	}
	if result := eventResult[primitives.ProcessExitResult](t, collected[len(collected)-1]); result.ExitCode != 7 {
		t.Fatalf("exit = %#v", result)
	}
}

func TestCancelProcessAllowsGracefulShutdown(t *testing.T) {
	directory := t.TempDir()
	ready := filepath.Join(directory, "ready")
	terminated := filepath.Join(directory, "terminated")
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			`trap '/bin/dd if=/dev/zero bs=1024 count=96 2>/dev/null; printf "terminated\n" > "$2"; exit 0' TERM; printf '%d\n' $$ > "$1"; while :; do :; done`,
			"sh",
			ready,
			terminated,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	waitForProcessPID(t, ready)

	cancel()
	if canceled := singleEvent(t, collectEvents(events), primitives.PrimitiveEventCanceled); canceled.Result != nil {
		t.Fatalf("canceled = %#v", canceled)
	}
	data, err := os.ReadFile(terminated)
	if err != nil {
		t.Fatalf("read termination marker: %v", err)
	}
	if string(data) != "terminated\n" {
		t.Fatalf("termination marker = %q", data)
	}
}

func TestBlockedInputWriteDefersCancellationUntilProcessStops(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "writer-started")
	processContext, cancelProcess := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(processContext, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"head -c 1 >/dev/null; printf '%d\n' $$ > \"$1\"; exec sleep 30",
			"sh",
			marker,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	writeContext, cancelWrite := context.WithCancel(t.Context())
	writeEvents := process.WriteInput(writeContext, primitives.ProcessWriteRequest{
		Source:        "operation-1",
		CorrelationID: "write-1",
		Data:          make([]byte, 1024*1024),
	})
	waitForProcessPID(t, marker)
	cancelWrite()
	cancelProcess()

	if canceled := singleEvent(t, collectEvents(events), primitives.PrimitiveEventCanceled); canceled.Result != nil {
		t.Fatalf("canceled = %#v", canceled)
	}
	failedWrite := singleEvent(t, collectEvents(writeEvents), primitives.PrimitiveEventProcessInputWriteFailed)
	if result := eventResult[primitives.ProcessWriteFailureResult](t, failedWrite); result.Count <= 0 || result.Error == "" {
		t.Fatalf("write failure = %#v", result)
	}
}

func TestProcessDeadlineWhileOutputIsBackpressured(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"/bin/dd if=/dev/zero bs=1024 count=96 2>/dev/null; sleep 30",
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("context error = %v", ctx.Err())
	}
	rest := collectEvents(events)
	canceled := rest[len(rest)-1]
	if canceled.Type != primitives.PrimitiveEventCanceled {
		t.Fatalf("terminal event = %#v", canceled)
	}
	if canceled.Result != nil {
		t.Fatalf("canceled = %#v", canceled)
	}
}

func TestCancelProcessTerminatesDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"sleep 30 & printf '%d\\n' $! > \"$1\"; wait",
			"sh",
			marker,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	descendantPID := waitForProcessPID(t, marker)
	descendantGone := cleanupProcessPID(t, descendantPID)
	cancel()
	if canceled := singleEvent(t, collectEvents(events), primitives.PrimitiveEventCanceled); canceled.Result != nil {
		t.Fatalf("canceled = %#v", canceled)
	}
	waitForProcessGone(t, descendantPID)
	descendantGone()
}

func TestProcessesFromSameSourceAreIndependent(t *testing.T) {
	first := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "printf first"},
	})
	second := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-2",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "printf second"},
	})

	assertCompletedProcessOutput(t, collectEvents(first.Events()), "operation-1", "process-1", "first")
	assertCompletedProcessOutput(t, collectEvents(second.Events()), "operation-1", "process-2", "second")
}

func TestProcessExitTerminatesDescendantsWithoutWaitingForInheritedOutputPipes(t *testing.T) {
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "sleep 30 & printf 'output:%d' \"$!\""},
	})
	events := process.Events()
	var collected []primitives.PrimitiveEvent
	var output []byte
	descendantPID := 0
	for {
		event := receiveProcessEvent(t, events)
		collected = append(collected, event)
		if event.Type == primitives.PrimitiveEventProcessOutput {
			output = append(output, eventResult[primitives.ProcessOutputResult](t, event).Data...)
			pid, err := strconv.Atoi(strings.TrimPrefix(string(output), "output:"))
			if err == nil {
				descendantPID = pid
			}
		}
		if event.Type == primitives.PrimitiveEventProcessExited {
			break
		}
	}
	if len(collected) != 3 || !strings.HasPrefix(string(output), "output:") {
		t.Fatalf("events = %#v", collected)
	}
	select {
	case trailing, open := <-events:
		if !open {
			t.Fatal("process closed the caller-owned event channel")
		}
		t.Fatalf("trailing event = %#v", trailing)
	default:
	}
	if descendantPID <= 0 {
		t.Fatalf("descendant PID = %d", descendantPID)
	}
	descendantGone := cleanupProcessPID(t, descendantPID)
	if err := syscall.Kill(descendantPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("inspect descendant after process exit: %v, want ESRCH", err)
	}
	descendantGone()
}

func TestProcessDrainsBufferedOutputAfterExit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"/bin/dd if=/dev/zero bs=1024 count=96 2>/dev/null; printf '%d\\n' $$ > \"$1\"; read -r line",
			"sh",
			marker,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	pid, outputBytes := waitForProcessPIDAndOutput(t, marker, events)
	processGone := cleanupProcessPID(t, pid)
	singleEvent(t, collectEvents(process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
		Source:        "operation-1",
		CorrelationID: "release-1",
		Data:          []byte("\n"),
	})), primitives.PrimitiveEventProcessInputWritten)
	waitForProcessGone(t, pid)
	processGone()

	rest := collectEvents(events)
	for _, event := range rest {
		if event.Type == primitives.PrimitiveEventProcessOutput {
			outputBytes += len(eventResult[primitives.ProcessOutputResult](t, event).Data)
		}
	}
	if outputBytes != 96*1024 {
		t.Fatalf("output = %d bytes, want %d", outputBytes, 96*1024)
	}
	if last := rest[len(rest)-1]; last.Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("last event = %#v", last)
	}
}

func waitForProcessPIDAndOutput(
	t *testing.T,
	path string,
	events <-chan primitives.PrimitiveEvent,
) (int, int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	delay := time.Millisecond
	outputBytes := 0
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, ready := parseProcessPID(data); ready {
				return pid, outputBytes
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read process PID: %v", err)
		}

		timer := time.NewTimer(delay)
		select {
		case event, ok := <-events:
			if !timer.Stop() {
				<-timer.C
			}
			if !ok {
				t.Fatal("process exited before recording its PID")
			}
			if event.Type != primitives.PrimitiveEventProcessOutput {
				t.Fatalf("event before process PID = %#v", event)
			}
			outputBytes += len(eventResult[primitives.ProcessOutputResult](t, event).Data)
		case <-timer.C:
		}
		delay = min(delay*2, 50*time.Millisecond)
	}
	t.Fatal("timed out waiting for process PID")
	return 0, 0
}

func TestProcessExitInterruptsPendingInputWrite(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "pid")
	release := filepath.Join(directory, "release")
	writeStarted := filepath.Join(directory, "write-started")
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"exec 3<&0; (head -c 1 >/dev/null; printf '%d\\n' $$ > \"$3\"; sleep 30) <&3 & printf '%d\\n' $! > \"$1\"; while [ ! -e \"$2\" ]; do sleep 0.01; done",
			"sh",
			marker,
			release,
			writeStarted,
		},
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	descendantPID := waitForProcessPID(t, marker)
	_ = cleanupProcessPID(t, descendantPID)
	writeEvents := process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
		Source:        "operation-1",
		CorrelationID: "write-1",
		Data:          make([]byte, 1024*1024),
	})
	waitForProcessPID(t, writeStarted)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release process: %v", err)
	}

	rest := collectProcessEvents(t, events)
	writeFailure := singleEvent(t, collectEvents(writeEvents), primitives.PrimitiveEventProcessInputWriteFailed)
	if result := eventResult[primitives.ProcessWriteFailureResult](t, writeFailure); result.Count <= 0 || result.Error == "" {
		t.Fatalf("pending write was not reported: %#v", writeFailure)
	}
	if last := rest[len(rest)-1]; last.Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("last event = %#v", last)
	}
}

func TestProcessUsesDirectoryAndEnvironment(t *testing.T) {
	directory := t.TempDir()
	canonicalDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	events := collectEvents(startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "printf '%s:%s:%s' \"$PWD\" \"$VALUE\" \"$1\"", "sh", "argument"},
		Directory:     directory,
		Environment:   []string{"VALUE=environment"},
	}).Events())
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	output := eventResult[primitives.ProcessOutputResult](t, events[1])
	if string(output.Data) != canonicalDirectory+":environment:argument" {
		t.Fatalf("output = %q", output.Data)
	}
}

func TestProcessEnvironmentInheritance(t *testing.T) {
	t.Setenv("HARNESS_TEST_PROCESS_VALUE", "inherited")
	t.Setenv("HARNESS_TEST_PROCESS_PARENT_ONLY", "parent")
	for _, test := range []struct {
		name        string
		environment []string
		want        string
	}{
		{name: "nil inherits", want: "inherited:parent"},
		{name: "empty", environment: []string{}, want: ":"},
		{name: "explicit replaces", environment: []string{"HARNESS_TEST_PROCESS_VALUE=explicit"}, want: "explicit:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := collectEvents(startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
				Source:        "operation-1",
				CorrelationID: "process-1",
				Path:          "/bin/sh",
				Arguments:     []string{"-c", `printf '%s:%s' "$HARNESS_TEST_PROCESS_VALUE" "$HARNESS_TEST_PROCESS_PARENT_ONLY"`},
				Environment:   test.environment,
			}).Events())
			if len(events) != 3 || events[0].Type != primitives.PrimitiveEventProcessStarted ||
				events[1].Type != primitives.PrimitiveEventProcessOutput ||
				events[2].Type != primitives.PrimitiveEventProcessExited {
				t.Fatalf("events = %#v", events)
			}
			output := eventResult[primitives.ProcessOutputResult](t, events[1])
			if string(output.Data) != test.want {
				t.Fatalf("output = %q, want %q", output.Data, test.want)
			}
			exit := eventResult[primitives.ProcessExitResult](t, events[2])
			if exit.ExitCode != 0 || exit.Signal != 0 {
				t.Fatalf("exit = %#v", exit)
			}
		})
	}
}

func TestProcessUsesExplicitEmptyEnvironment(t *testing.T) {
	events := collectEvents(startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/usr/bin/env",
		Environment:   []string{},
	}).Events())
	if len(events) != 2 || events[0].Type != primitives.PrimitiveEventProcessStarted ||
		events[1].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", events)
	}
	exit := eventResult[primitives.ProcessExitResult](t, events[1])
	if exit.ExitCode != 0 || exit.Signal != 0 {
		t.Fatalf("exit = %#v", exit)
	}
}

func TestProcessSelectsParentPipes(t *testing.T) {
	events := collectEvents(startProcess(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"if read -r value; then printf 'stdin:%s' \"$value\"; else printf stdout; fi; printf stderr >&2",
		},
		Pipes: primitives.ProcessPipeStdout,
	}).Events())
	if len(events) != 3 || events[0].Type != primitives.PrimitiveEventProcessStarted ||
		events[1].Type != primitives.PrimitiveEventProcessOutput ||
		events[2].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", events)
	}
	output := eventResult[primitives.ProcessOutputResult](t, events[1])
	if output.Stream != primitives.ProcessStdout || string(output.Data) != "stdout" {
		t.Fatalf("output = %#v", output)
	}
}

func TestProcessUsesNoParentPipesByDefault(t *testing.T) {
	events := collectEvents(startProcess(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/usr/bin/true",
	}).Events())
	if len(events) != 2 || events[0].Type != primitives.PrimitiveEventProcessStarted ||
		events[1].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", events)
	}
	if result := eventResult[primitives.ProcessExitResult](t, events[1]); result.ExitCode != 0 {
		t.Fatalf("exit = %#v", result)
	}
}

func TestProcessWithoutStdinPipeClosesInputIdempotently(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcess(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sleep",
		Arguments:     []string{"30"},
		Pipes:         primitives.ProcessPipeStdout | primitives.ProcessPipeStderr,
	})
	events := process.Events()
	if started := receiveProcessEvent(t, events); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	closed := singleEvent(t, collectEvents(process.CloseInput(t.Context(), primitives.ProcessCloseInputRequest{
		Source:        "operation-1",
		CorrelationID: "close-1",
	})), primitives.PrimitiveEventProcessInputClosed)
	if closed.Result != nil {
		t.Fatalf("close result = %#v", closed.Result)
	}
	cancel()
	singleEvent(t, collectEvents(events), primitives.PrimitiveEventCanceled)
}

func TestProcessRejectsInvalidPipeSelection(t *testing.T) {
	event := singleEvent(t, collectEvents(startProcess(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/true",
		Pipes:         primitives.ProcessPipeSet(1 << 7),
	}).Events()), primitives.PrimitiveEventFailed)
	failure := eventResult[primitives.PrimitiveFailureResult](t, event)
	if !strings.Contains(failure.Error, "pipe selection") {
		t.Fatalf("failure = %q", failure.Error)
	}
}

func TestProcessRequiresAbsoluteExecutablePath(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "requested-command")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf unexpected"), 0o700); err != nil {
		t.Fatalf("create executable: %v", err)
	}
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "requested-command",
		Environment:   []string{"PATH=" + directory},
	})

	event := singleEvent(t, collectEvents(process.Events()), primitives.PrimitiveEventFailed)
	if failure := eventResult[primitives.PrimitiveFailureResult](t, event); !strings.Contains(failure.Error, "path must be absolute") {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestProcessStartFailures(t *testing.T) {
	tests := []struct {
		name    string
		request primitives.ProcessStartRequest
		want    string
	}{
		{
			name:    "empty path",
			request: primitives.ProcessStartRequest{Source: "operation-1", CorrelationID: "process-1"},
			want:    "path must be set",
		},
		{
			name: "missing executable",
			request: primitives.ProcessStartRequest{
				Source:        "operation-1",
				CorrelationID: "process-1",
				Path:          "/missing/process/executable",
			},
			want: "start process",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := singleEvent(
				t,
				collectEvents(startProcessWithAllPipes(t.Context(), test.request).Events()),
				primitives.PrimitiveEventFailed,
			)
			failure := eventResult[primitives.PrimitiveFailureResult](t, event)
			if !strings.Contains(failure.Error, test.want) {
				t.Fatalf("error = %q, want substring %q", failure.Error, test.want)
			}
		})
	}
}

func TestStartProcessWithCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
	})
	event := singleEvent(t, collectEvents(process.Events()), primitives.PrimitiveEventCanceled)
	if event.Result != nil {
		t.Fatalf("result = %#v", event.Result)
	}
	control := singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:        "operation-1",
		CorrelationID: "signal-1",
		Signal:        syscall.SIGTERM,
	})), primitives.PrimitiveEventFailed)
	if failure := eventResult[primitives.PrimitiveFailureResult](t, control); !strings.Contains(failure.Error, "context canceled") {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestProcessControlFailsAfterStartFailure(t *testing.T) {
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/missing/process/executable",
	})
	event := singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:        "operation-1",
		CorrelationID: "signal-1",
		Signal:        syscall.SIGTERM,
	})), primitives.PrimitiveEventFailed)
	if failure := eventResult[primitives.PrimitiveFailureResult](t, event); !strings.Contains(failure.Error, "start process") {
		t.Fatalf("failure = %#v", failure)
	}
	_ = collectEvents(process.Events())
}

func TestProcessControlsFailAfterExitWithUnreadEvents(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	process := startProcessWithAllPipes(t.Context(), primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments: []string{
			"-c",
			"printf '%d\\n' $$ > \"$1\"; printf output",
			"sh",
			marker,
		},
	})
	pid := waitForProcessPID(t, marker)
	waitForProcessGone(t, pid)

	controlEvents := []struct {
		correlationID primitives.CorrelationID
		eventType     primitives.PrimitiveEventType
		events        <-chan primitives.PrimitiveEvent
	}{
		{correlationID: "write-1", eventType: primitives.PrimitiveEventProcessInputWriteFailed, events: process.WriteInput(t.Context(), primitives.ProcessWriteRequest{
			Source:        "operation-1",
			CorrelationID: "write-1",
			Data:          []byte("input"),
		})},
		{correlationID: "close-1", eventType: primitives.PrimitiveEventFailed, events: process.CloseInput(t.Context(), primitives.ProcessCloseInputRequest{
			Source:        "operation-1",
			CorrelationID: "close-1",
		})},
		{correlationID: "signal-1", eventType: primitives.PrimitiveEventFailed, events: process.Signal(t.Context(), primitives.ProcessSignalRequest{
			Source:        "operation-1",
			CorrelationID: "signal-1",
			Signal:        syscall.SIGTERM,
		})},
		{correlationID: "signal-group-1", eventType: primitives.PrimitiveEventFailed, events: process.Signal(t.Context(), primitives.ProcessSignalRequest{
			Source:              "operation-1",
			CorrelationID:       "signal-group-1",
			Signal:              syscall.SIGTERM,
			PropagateToChildren: true,
		})},
	}
	for _, control := range controlEvents {
		event := singleEvent(t, collectEvents(control.events), control.eventType)
		if event.Source != "operation-1" || event.CorrelationID != control.correlationID {
			t.Fatalf("control event = %#v", event)
		}
		var failure string
		if event.Type == primitives.PrimitiveEventProcessInputWriteFailed {
			failure = eventResult[primitives.ProcessWriteFailureResult](t, event).Error
		} else {
			failure = eventResult[primitives.PrimitiveFailureResult](t, event).Error
		}
		if !strings.Contains(failure, "process invocation is done") {
			t.Fatalf("failure = %q", failure)
		}
	}
	assertCompletedProcessOutput(
		t,
		collectEvents(process.Events()),
		"operation-1",
		"process-1",
		"output",
	)
}

func TestProcessSignalFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "exec sleep 30"},
	})
	if started := receiveProcessEvent(t, process.Events()); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	event := singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:              "operation-1",
		CorrelationID:       "signal-1",
		Signal:              syscall.Signal(-1),
		PropagateToChildren: true,
	})), primitives.PrimitiveEventFailed)
	if failure := eventResult[primitives.PrimitiveFailureResult](t, event); !strings.Contains(failure.Error, "invalid argument") {
		t.Fatalf("failure = %#v", failure)
	}
	cancel()
	_ = collectEvents(process.Events())
}

func TestProcessControlHonorsItsContext(t *testing.T) {
	ctx, cancelProcess := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "exec sleep 30"},
	})
	if started := receiveProcessEvent(t, process.Events()); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}

	commandContext, cancelCommand := context.WithCancel(t.Context())
	cancelCommand()
	controls := []struct {
		correlationID primitives.CorrelationID
		events        <-chan primitives.PrimitiveEvent
	}{
		{correlationID: "write-1", events: process.WriteInput(commandContext, primitives.ProcessWriteRequest{
			Source:        "operation-1",
			CorrelationID: "write-1",
			Data:          []byte("input"),
		})},
		{correlationID: "close-1", events: process.CloseInput(commandContext, primitives.ProcessCloseInputRequest{
			Source:        "operation-1",
			CorrelationID: "close-1",
		})},
		{correlationID: "signal-1", events: process.Signal(commandContext, primitives.ProcessSignalRequest{
			Source:        "operation-1",
			CorrelationID: "signal-1",
			Signal:        syscall.SIGTERM,
		})},
	}
	for _, control := range controls {
		event := singleEvent(t, collectEvents(control.events), primitives.PrimitiveEventCanceled)
		if event.Source != "operation-1" || event.CorrelationID != control.correlationID {
			t.Fatalf("event = %#v", event)
		}
	}
	cancelProcess()
	_ = collectEvents(process.Events())
}

func TestProcessControlFailsAfterProcessCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	process := startProcessWithAllPipes(ctx, primitives.ProcessStartRequest{
		Source:        "operation-1",
		CorrelationID: "process-1",
		Path:          "/bin/sh",
		Arguments:     []string{"-c", "exec sleep 30"},
	})
	if started := receiveProcessEvent(t, process.Events()); started.Type != primitives.PrimitiveEventProcessStarted {
		t.Fatalf("started = %#v", started)
	}
	cancel()
	_ = collectEvents(process.Events())
	event := singleEvent(t, collectEvents(process.Signal(t.Context(), primitives.ProcessSignalRequest{
		Source:        "operation-1",
		CorrelationID: "signal-1",
		Signal:        syscall.SIGTERM,
	})), primitives.PrimitiveEventFailed)
	if failure := eventResult[primitives.PrimitiveFailureResult](t, event); !strings.Contains(failure.Error, "process invocation is done") {
		t.Fatalf("failure = %#v", failure)
	}
}

func assertCompletedProcessOutput(
	t *testing.T,
	events []primitives.PrimitiveEvent,
	source primitives.SourceID,
	correlationID primitives.CorrelationID,
	want string,
) {
	t.Helper()
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	assertProcessEventIdentity(t, events, source, correlationID)
	if events[0].Type != primitives.PrimitiveEventProcessStarted ||
		events[1].Type != primitives.PrimitiveEventProcessOutput ||
		events[2].Type != primitives.PrimitiveEventProcessExited {
		t.Fatalf("events = %#v", events)
	}
	if output := eventResult[primitives.ProcessOutputResult](t, events[1]); string(output.Data) != want {
		t.Fatalf("output = %q, want %q", output.Data, want)
	}
}

func assertProcessEventIdentity(
	t *testing.T,
	events []primitives.PrimitiveEvent,
	source primitives.SourceID,
	correlationID primitives.CorrelationID,
) {
	t.Helper()
	for _, event := range events {
		if event.Source != source || event.CorrelationID != correlationID {
			t.Fatalf("event identity = (%q, %q), want (%q, %q)", event.Source, event.CorrelationID, source, correlationID)
		}
	}
}

func assertProcessEventTypes(
	t *testing.T,
	events []primitives.PrimitiveEvent,
	want map[primitives.PrimitiveEventType]int,
) {
	t.Helper()
	got := make(map[primitives.PrimitiveEventType]int)
	for _, event := range events {
		got[event.Type]++
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %#v, want %#v", got, want)
	}
	for eventType, count := range want {
		if got[eventType] != count {
			t.Fatalf("event %q count = %d, want %d", eventType, got[eventType], count)
		}
	}
}

func receiveProcessEvent(t *testing.T, events <-chan primitives.PrimitiveEvent) primitives.PrimitiveEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("process event channel closed")
		}
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for process event")
		return primitives.PrimitiveEvent{}
	}
}

func collectProcessEvents(
	t *testing.T,
	events <-chan primitives.PrimitiveEvent,
) []primitives.PrimitiveEvent {
	t.Helper()
	var collected []primitives.PrimitiveEvent
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return collected
			}
			collected = append(collected, event)
			if event.Type == primitives.PrimitiveEventProcessExited ||
				event.Type == primitives.PrimitiveEventFailed ||
				event.Type == primitives.PrimitiveEventCanceled {
				return collected
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for process completion")
			return nil
		}
	}
}

func waitForProcessPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	delay := time.Millisecond
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, ready := parseProcessPID(data); ready {
				return pid
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read process PID: %v", err)
		}
		time.Sleep(delay)
		delay = min(delay*2, 50*time.Millisecond)
	}
	t.Fatal("timed out waiting for process PID")
	return 0
}

func waitForFileContent(t *testing.T, path string, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	delay := time.Millisecond
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && string(data) == want {
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read file: %v", err)
		}
		time.Sleep(delay)
		delay = min(delay*2, 50*time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to contain %q", path, want)
}

func parseProcessPID(data []byte) (int, bool) {
	marker := string(data)
	if !strings.HasSuffix(marker, "\n") {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(marker))
	return pid, err == nil && pid > 0
}

func TestParseProcessPID(t *testing.T) {
	tests := []struct {
		marker string
		pid    int
		ready  bool
	}{
		{marker: ""},
		{marker: "12"},
		{marker: "not-a-pid\n"},
		{marker: "0\n"},
		{marker: "1234\n", pid: 1234, ready: true},
	}
	for _, test := range tests {
		pid, ready := parseProcessPID([]byte(test.marker))
		if pid != test.pid || ready != test.ready {
			t.Fatalf("parse PID %q = (%d, %t), want (%d, %t)", test.marker, pid, ready, test.pid, test.ready)
		}
	}
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	delay := time.Millisecond
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("inspect process: %v", err)
		}
		time.Sleep(delay)
		delay = min(delay*2, 50*time.Millisecond)
	}
	t.Fatal("timed out waiting for process exit")
}

func cleanupProcessPID(t *testing.T, pid int) func() {
	t.Helper()
	processGone := false
	t.Cleanup(func() {
		if processGone {
			return
		}
		for _, signal := range []syscall.Signal{syscall.SIGCONT, syscall.SIGKILL} {
			if err := syscall.Kill(pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Errorf("signal process %d with %s: %v", pid, signal, err)
			}
		}
	})
	return func() {
		processGone = true
	}
}
