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

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

type logLine struct {
	stream string
	text   string
}

type lineMsg struct {
	line logLine
}

type doneMsg struct {
	exitCode int
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

type model struct {
	buffer      *ringBuffer
	height      int
	termWidth   int
	termHeight  int
	commandText string
	done        bool
	exitCode    int
}

func newModel(height int, commandText string) model {
	return model{
		buffer:      newRingBuffer(height),
		height:      height,
		commandText: commandText,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = v.Width
		m.termHeight = v.Height
		return m, nil
	case lineMsg:
		m.buffer.add(v.line)
		return m, nil
	case doneMsg:
		m.done = true
		m.exitCode = v.exitCode
		return m, tea.Quit
	default:
		return m, nil
	}
}

func (m model) View() string {
	header := fmt.Sprintf("logbox running: %s", m.commandText)
	lines := m.buffer.lines()

	visible := m.height
	if m.termHeight > 0 {
		maxByWindow := m.termHeight - 2
		if maxByWindow < 1 {
			maxByWindow = 1
		}
		if visible > maxByWindow {
			visible = maxByWindow
		}
	}

	rendered := make([]string, 0, visible)
	start := 0
	if len(lines) > visible {
		start = len(lines) - visible
	}
	for _, line := range lines[start:] {
		rendered = append(rendered, line.text)
	}
	for len(rendered) < visible {
		rendered = append(rendered, "")
	}

	content := header + "\n" + strings.Join(rendered, "\n")
	if m.termHeight <= 0 {
		return content
	}

	lineCount := 1 + visible
	pad := m.termHeight - lineCount
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat("\n", pad) + content
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
		fmt.Fprintln(os.Stderr, "usage: logbox --height 10 [--clear] [--plain] -- <command> [args...]")
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
	fs.IntVar(&opts.height, "height", 10, "tail lines to keep in the live view")
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
	program := tea.NewProgram(newModel(opts.height, commandText), tea.WithInput(nil))

	lines := make(chan logLine, opts.height*2)
	var readWG sync.WaitGroup
	readWG.Add(2)
	go readLines(stdout, "stdout", lines, &readWG)
	go readLines(stderr, "stderr", lines, &readWG)

	go func() {
		readWG.Wait()
		close(lines)
	}()

	go func() {
		for line := range lines {
			program.Send(lineMsg{line: line})
		}
	}()

	go func() {
		err := cmd.Wait()
		stopSignals()
		program.Send(doneMsg{exitCode: exitCodeFromError(err)})
	}()

	finalModel, runErr := program.Run()
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "tui failed: %v\n", runErr)
		return 1
	}

	m, ok := finalModel.(model)
	if !ok {
		fmt.Fprintln(os.Stderr, "internal error: invalid final model")
		return 1
	}

	if !opts.clear {
		fmt.Printf("logbox finished: %s (exit %d)\n", commandText, m.exitCode)
		for _, line := range m.buffer.lines() {
			fmt.Println(line.text)
		}
	}

	return m.exitCode
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
