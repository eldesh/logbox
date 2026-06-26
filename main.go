package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

type logLine struct {
	stream string
	text   string
}

type ringBuffer struct {
	buf      []logLine
	start    int
	size     int
	capacity int
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &ringBuffer{
		buf:      make([]logLine, capacity),
		capacity: capacity,
	}
}

func (r *ringBuffer) add(line logLine) {
	if r.size < r.capacity {
		r.buf[(r.start+r.size)%r.capacity] = line
		r.size++
		return
	}
	r.buf[r.start] = line
	r.start = (r.start + 1) % r.capacity
}

func (r *ringBuffer) lines() []logLine {
	out := make([]logLine, 0, r.size)
	for i := 0; i < r.size; i++ {
		out = append(out, r.buf[(r.start+i)%r.capacity])
	}
	return out
}

type options struct {
	height int
	clear  bool
	plain  bool
}

func main() {
	opts, command, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: logbox [--height N] [--clear] [--plain] -- <command> [args...]")
		os.Exit(2)
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	if opts.plain || !isTTY {
		os.Exit(runPlain(command))
	}

	os.Exit(runTUI(command, opts))
}

func parseArgs(args []string) (options, []string, error) {
	idx := -1
	for i, a := range args {
		if a == "--" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return options{}, nil, errors.New("missing command separator --")
	}

	fs := flag.NewFlagSet("logbox", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := options{}
	fs.IntVar(&opts.height, "height", autoHeightDefault(), "tail lines to keep in the live view")
	fs.BoolVar(&opts.clear, "clear", false, "clear view on exit instead of printing final tail")
	fs.BoolVar(&opts.plain, "plain", false, "disable TTY live rendering")
	if err := fs.Parse(args[:idx]); err != nil {
		return options{}, nil, err
	}
	if opts.height < 1 {
		return options{}, nil, errors.New("height must be >= 1")
	}
	cmdArgs := args[idx+1:]
	if len(cmdArgs) == 0 {
		return options{}, nil, errors.New("missing command after --")
	}
	return opts, cmdArgs, nil
}

func autoHeightDefault() int {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return 10
	}

	_, h, err := term.GetSize(fd)
	if err != nil || h <= 0 {
		return 10
	}

	defaultHeight := h / 3
	if defaultHeight < 5 {
		defaultHeight = 5
	}
	if defaultHeight > 30 {
		defaultHeight = 30
	}

	return defaultHeight
}

func runPlain(command []string) int {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start command: %v\n", err)
		return 1
	}

	stopSignals := forwardSignals(cmd)
	err := cmd.Wait()
	stopSignals()
	return exitCodeFromError(err)
}

func runTUI(command []string, opts options) int {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open stdout pipe: %v\n", err)
		return 1
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open stderr pipe: %v\n", err)
		return 1
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start command: %v\n", err)
		return 1
	}

	stopSignals := forwardSignals(cmd)
	commandText := strings.Join(command, " ")
	buffer := newRingBuffer(opts.height)
	renderer := newRegionRenderer(os.Stdout, opts.height)
	renderer.reserve()
	renderer.render("logbox running: "+commandText, buffer.lines())

	lines := make(chan logLine, opts.height*2)
	doneCh := make(chan int, 1)
	var readWG sync.WaitGroup
	readWG.Add(2)
	go readLines(stdout, "stdout", lines, &readWG)
	go readLines(stderr, "stderr", lines, &readWG)

	go func() {
		readWG.Wait()
		close(lines)
	}()

	go func() {
		err := cmd.Wait()
		stopSignals()
		doneCh <- exitCodeFromError(err)
	}()

	exitCode := 1
	processDone := false
	for lines != nil || !processDone {
		select {
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			buffer.add(line)
			renderer.render("logbox running: "+commandText, buffer.lines())
		case code := <-doneCh:
			exitCode = code
			processDone = true
		}
	}

	if opts.clear {
		renderer.clear()
	} else {
		renderer.render(fmt.Sprintf("logbox finished: %s (exit %d)", commandText, exitCode), buffer.lines())
	}

	return exitCode
}

type regionRenderer struct {
	out    io.Writer
	height int
}

func newRegionRenderer(out io.Writer, height int) *regionRenderer {
	if height < 1 {
		height = 1
	}
	return &regionRenderer{out: out, height: height}
}

func (r *regionRenderer) reserve() {
	fmt.Fprint(r.out, strings.Repeat("\n", r.height))
}

func (r *regionRenderer) render(status string, lines []logLine) {
	rows := make([]string, 0, r.height)
	rows = append(rows, status)

	availableLogs := r.height - 1
	if availableLogs > 0 {
		start := 0
		if len(lines) > availableLogs {
			start = len(lines) - availableLogs
		}
		for _, line := range lines[start:] {
			rows = append(rows, line.text)
		}
	}

	for len(rows) < r.height {
		rows = append(rows, "")
	}

	fmt.Fprintf(r.out, "\x1b[%dA", r.height)
	for i := 0; i < r.height; i++ {
		fmt.Fprint(r.out, "\r\x1b[2K")
		fmt.Fprint(r.out, rows[i])
		if i < r.height-1 {
			fmt.Fprint(r.out, "\n")
		}
	}
	fmt.Fprint(r.out, "\x1b[1B\r")
}

func (r *regionRenderer) clear() {
	fmt.Fprintf(r.out, "\x1b[%dA", r.height)
	for i := 0; i < r.height; i++ {
		fmt.Fprint(r.out, "\r\x1b[2K")
		if i < r.height-1 {
			fmt.Fprint(r.out, "\n")
		}
	}
	fmt.Fprint(r.out, "\x1b[1B\r")
}

func readLines(r io.Reader, stream string, out chan<- logLine, wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		out <- logLine{stream: stream, text: scanner.Text()}
	}
}

func forwardSignals(cmd *exec.Cmd) func() {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case sig := <-sigCh:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()

	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return 128 + int(status.Signal())
			}
			return status.ExitStatus()
		}
	}

	return 1
}
