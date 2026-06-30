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
	"time"

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

func (r *ringBuffer) add(line logLine) bool {
	if r.size < r.capacity {
		r.buf[(r.start+r.size)%r.capacity] = line
		r.size++
		return false
	}
	r.buf[r.start] = line
	r.start = (r.start + 1) % r.capacity
	return true
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
	history int
	clear  bool
	plain  bool
	tee    string
	append bool
}

type navCommand int

const (
	navUp navCommand = iota
	navDown
	navPageUp
	navPageDown
	navTop
	navBottom
	navFollow
	navQuit
)

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"

	ansiFgGreen = "\x1b[38;5;22m"
	ansiFgWhite = "\x1b[38;5;231m"
	ansiFgDarkRed = "\x1b[38;5;88m"

	ansiBgGreen    = "\x1b[48;5;148m"
	ansiBgGray     = "\x1b[48;5;239m"
	ansiBgSoftRed  = "\x1b[48;5;217m"
)

const (
	statusStyleLead = ansiBold + ansiFgGreen + ansiBgGreen
	statusStyleInfo = ansiFgWhite + ansiBgGray
)

type inputController struct {
	commands chan navCommand
	tty      *os.File
	closeTTY bool
	oldState *term.State
	done     chan struct{}
	stopOnce sync.Once
}

func startInputController() (*inputController, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		return startInputControllerWithTTY(tty, true)
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		return startInputControllerWithTTY(os.Stdin, false)
	}

	return nil, fmt.Errorf("keyboard input unavailable: failed to open /dev/tty (%v)", err)
}

func startInputControllerWithTTY(tty *os.File, closeTTY bool) (*inputController, error) {
	fd := int(tty.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		if closeTTY {
			_ = tty.Close()
		}
		return nil, err
	}

	ic := &inputController{
		commands: make(chan navCommand, 32),
		tty:      tty,
		closeTTY: closeTTY,
		oldState: oldState,
		done:     make(chan struct{}),
	}

	go ic.readLoop()
	return ic, nil
}

func (ic *inputController) stop() {
	ic.stopOnce.Do(func() {
		close(ic.done)
		if ic.oldState != nil {
			_ = term.Restore(int(ic.tty.Fd()), ic.oldState)
		}
		if ic.closeTTY {
			_ = ic.tty.Close()
		}
	})
}

func (ic *inputController) emit(cmd navCommand) {
	select {
	case ic.commands <- cmd:
	default:
	}
}

func (ic *inputController) readLoop() {
	defer close(ic.commands)

	r := bufio.NewReader(ic.tty)
	for {
		select {
		case <-ic.done:
			return
		default:
		}

		b, err := r.ReadByte()
		if err != nil {
			return
		}

		switch b {
		case 'k':
			ic.emit(navUp)
		case 'j':
			ic.emit(navDown)
		case 'u':
			ic.emit(navPageUp)
		case 'd':
			ic.emit(navPageDown)
		case 'g':
			ic.emit(navTop)
		case 'G':
			ic.emit(navBottom)
		case 'f', 'F':
			ic.emit(navFollow)
		case 'q', 'Q', '\r', '\n':
			ic.emit(navQuit)
		case '\x1b':
			next, err := r.ReadByte()
			if err != nil {
				return
			}
			if next == 'O' {
				code, err := r.ReadByte()
				if err != nil {
					return
				}
				switch code {
				case 'A':
					ic.emit(navUp)
				case 'B':
					ic.emit(navDown)
				case 'H':
					ic.emit(navTop)
				case 'F':
					ic.emit(navFollow)
				}
				continue
			}
			if next != '[' {
				continue
			}

			code, err := r.ReadByte()
			if err != nil {
				return
			}
			switch code {
			case 'A':
				ic.emit(navUp)
			case 'B':
				ic.emit(navDown)
			case 'H':
				ic.emit(navTop)
			case 'F':
				ic.emit(navFollow)
			case '5':
				trail, err := r.ReadByte()
				if err != nil {
					return
				}
				if trail == '~' {
					ic.emit(navPageUp)
				}
			case '6':
				trail, err := r.ReadByte()
				if err != nil {
					return
				}
				if trail == '~' {
					ic.emit(navPageDown)
				}
			}
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func calcWindow(total, available, start int, follow bool) (int, int, int) {
	if available < 0 {
		available = 0
	}
	maxStart := maxInt(0, total-available)
	if follow {
		start = maxStart
	} else {
		start = clampInt(start, 0, maxStart)
	}
	end := start + available
	if end > total {
		end = total
	}
	return start, end, maxStart
}

func colorizeStatus(text, style string) string {
	return style + text + ansiReset
}

func buildStatusLine(lead, info string) string {
	line := colorizeStatus(" "+lead+" ", statusStyleLead)
	line += colorizeStatus(" "+info, statusStyleInfo)
	return line
}

func modeText(follow bool) string {
	if follow {
		return "FOLLOW"
	}
	return "SCROLL"
}

func viewRangeText(total, available, start int, follow bool) string {
	if total <= 0 || available <= 0 {
		return "view: 0-0/0"
	}
	viewStart, viewEnd, _ := calcWindow(total, available, start, follow)
	if viewEnd <= viewStart {
		return fmt.Sprintf("view: 0-0/%d", total)
	}
	return fmt.Sprintf("view: %d-%d/%d", viewStart+1, viewEnd, total)
}

func elapsedText(startedAt, now time.Time) string {
	d := now.Sub(startedAt)
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func exitText(exitCode int) string {
	return fmt.Sprintf("exit %d", exitCode)
}

func exitFieldForInfo(exitCode int) string {
	text := " " + exitText(exitCode) + " "
	if exitCode == 0 {
		return text
	}
	// Keep separators uncolored; only the final exit field gets a softer red accent.
	return ansiFgDarkRed + ansiBgSoftRed + text + ansiReset
}

func statusForCommand(follow bool, startedAt time.Time, lineCount, available, start int, now time.Time) string {
	return buildStatusLine(
		"RUNNING",
		fmt.Sprintf("%s | %s | %s ", modeText(follow), viewRangeText(lineCount, available, start, follow), elapsedText(startedAt, now)),
	)
}

func statusForStdin(follow bool, startedAt time.Time, lineCount, available, start int, now time.Time) string {
	return buildStatusLine(
		"READING",
		fmt.Sprintf("stdin | %s | %s | %s ", modeText(follow), viewRangeText(lineCount, available, start, follow), elapsedText(startedAt, now)),
	)
}


func statusFinishedForCommand(exitCode int, follow bool, hold bool, startedAt, finishedAt time.Time, lineCount, available, start int) string {
	mode := modeText(follow)
	if hold {
		mode = "SCROLL"
	}
	info := fmt.Sprintf("%s | %s | %s |", mode, viewRangeText(lineCount, available, start, follow), elapsedText(startedAt, finishedAt))
	if hold {
		return buildStatusLine("FINISHED", info+exitFieldForInfo(exitCode))
	}
	return buildStatusLine("FINISHED", info+exitFieldForInfo(exitCode))
}

func statusFinishedForStdin(follow bool, hold bool, startedAt, finishedAt time.Time, lineCount, available, start int) string {
	mode := modeText(follow)
	if hold {
		mode = "SCROLL"
	}
	return buildStatusLine("FINISHED", fmt.Sprintf("stdin | %s | %s | %s | %s", mode, viewRangeText(lineCount, available, start, follow), elapsedText(startedAt, finishedAt), exitText(0)))
}

func applyNav(cmd navCommand, linesCount, available, start int, follow bool) (int, bool) {
	currentStart, _, maxStart := calcWindow(linesCount, available, start, follow)
	start = currentStart
	page := maxInt(1, available)

	switch cmd {
	case navUp:
		follow = false
		start--
	case navDown:
		follow = false
		start++
	case navPageUp:
		follow = false
		start -= page
	case navPageDown:
		follow = false
		start += page
	case navTop:
		follow = false
		start = 0
	case navBottom:
		follow = false
		start = maxStart
	case navFollow:
		follow = true
		start = maxStart
	}

	start = clampInt(start, 0, maxStart)
	if follow {
		start = maxStart
	}
	return start, follow
}

func main() {
	opts, command, hasCommand, err := parseArgs(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stdout)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		printUsage(os.Stderr)
		os.Exit(2)
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))

	if !hasCommand {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(os.Stderr, "no command specified and stdin is interactive")
			os.Exit(2)
		}

		if opts.plain || !isTTY {
			os.Exit(runStdinPlain(opts))
		}

		os.Exit(runStdinTUI(opts))
	}

	if opts.plain || !isTTY {
		os.Exit(runPlainWithOptions(command, opts))
	}

	os.Exit(runTUI(command, opts))
}

func parseArgs(args []string) (options, []string, bool, error) {
	idx := -1
	for i, a := range args {
		if a == "--" {
			idx = i
			break
		}
	}

	fs := flag.NewFlagSet("logbox", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := options{}
	fs.IntVar(&opts.height, "height", autoHeightDefault(), "tail lines to keep in the live view")
	fs.IntVar(&opts.history, "history", 1000, "lines to keep in scrollback history for TUI mode")
	fs.BoolVar(&opts.clear, "clear", false, "clear view on exit instead of printing final tail")
	fs.BoolVar(&opts.plain, "plain", false, "disable TTY live rendering")
	fs.StringVar(&opts.tee, "tee", "", "write command output to file while still showing it")
	fs.BoolVar(&opts.append, "append", false, "append to --tee file instead of truncating")
	parseTarget := args
	if idx >= 0 {
		parseTarget = args[:idx]
	}
	if err := fs.Parse(parseTarget); err != nil {
		return options{}, nil, false, err
	}
	if opts.height < 1 {
		return options{}, nil, false, errors.New("height must be >= 1")
	}
	if opts.history < opts.height {
		return options{}, nil, false, errors.New("history must be >= height")
	}
	if opts.append && opts.tee == "" {
		return options{}, nil, false, errors.New("--append requires --tee")
	}

	if idx == -1 {
		if len(fs.Args()) > 0 {
			return options{}, nil, false, errors.New("command must follow --")
		}
		return opts, nil, false, nil
	}

	cmdArgs := args[idx+1:]
	if len(cmdArgs) == 0 {
		return options{}, nil, false, errors.New("missing command after --")
	}
	return opts, cmdArgs, true, nil
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "logbox: tail-style live log wrapper")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  logbox [flags] -- <command> [args...]")
	fmt.Fprintln(out, "  producer | logbox [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Flags:")
	fmt.Fprintln(out, "  --height N    Live view height in lines (status line + visible logs)")
	fmt.Fprintln(out, "               Default: auto (about 1/3 of terminal, min 5, max 30)")
	fmt.Fprintln(out, "  --history N   Scrollback buffer size in lines for TUI mode")
	fmt.Fprintln(out, "               Default: 1000 (must be >= --height)")
	fmt.Fprintln(out, "  --clear       Clear the reserved live region on exit")
	fmt.Fprintln(out, "               Default: keep final status/log lines")
	fmt.Fprintln(out, "  --plain       Disable TUI redraw and pass output through as plain logs")
	fmt.Fprintln(out, "  --tee FILE    Also write output to FILE")
	fmt.Fprintln(out, "  --append      Append to --tee FILE instead of truncating")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "TUI Keys (while running):")
	fmt.Fprintln(out, "  k / Up         Scroll up one line (switches to SCROLL mode)")
	fmt.Fprintln(out, "  j / Down       Scroll down one line")
	fmt.Fprintln(out, "  u / PageUp     Scroll up one page")
	fmt.Fprintln(out, "  d / PageDown   Scroll down one page")
	fmt.Fprintln(out, "  g              Jump to top of buffer")
	fmt.Fprintln(out, "  G              Jump to bottom of buffer")
	fmt.Fprintln(out, "  f              Return to FOLLOW mode (tail-follow)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "After Process Exit:")
	fmt.Fprintln(out, "  FOLLOW mode    logbox exits immediately")
	fmt.Fprintln(out, "  SCROLL mode    logbox stays open for review")
	fmt.Fprintln(out, "               q/Enter: quit, f: jump to end and quit")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  logbox --height 10 -- make test")
	fmt.Fprintln(out, "  logbox --height 10 --history 2000 -- make test")
	fmt.Fprintln(out, "  logbox --height 14 --history 12000 -- bash -lc 'make lint && make test && make build'")
	fmt.Fprintln(out, "  logbox --tee build.log --append -- docker build --progress=plain .")
	fmt.Fprintln(out, "  cat app.log | logbox --plain --tee out.log")
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

func runPlainWithOptions(command []string, opts options) int {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin

	closeTee, teeWriter, err := setupTeeWriter(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open tee file: %v\n", err)
		return 1
	}
	defer closeTee()

	if teeWriter != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, teeWriter)
		cmd.Stderr = io.MultiWriter(os.Stderr, teeWriter)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start command: %v\n", err)
		return 1
	}

	stopSignals := forwardSignals(cmd)
	err = cmd.Wait()
	stopSignals()
	return exitCodeFromError(err)
}

func runStdinPlain(opts options) int {
	closeTee, teeWriter, err := setupTeeWriter(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open tee file: %v\n", err)
		return 1
	}
	defer closeTee()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(os.Stdout, line)
		if teeWriter != nil {
			_, _ = fmt.Fprintln(teeWriter, line)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
		return 1
	}

	return 0
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

	buffer := newRingBuffer(opts.history)
	renderer := newRegionRenderer(os.Stdout, opts.height)
	closeTee, teeWriter, err := setupTeeWriter(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open tee file: %v\n", err)
		return 1
	}
	defer closeTee()

	renderer.reserve()
	follow := true
	viewStart := 0
	lineCount := 0
	startedAt := time.Now()
	finishedAt := startedAt
	available := opts.height - 1
	renderer.render(statusForCommand(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)

	inputController, err := startInputController()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: keyboard navigation disabled: %v\n", err)
		inputController = nil
	}
	if inputController != nil {
		defer inputController.stop()
	}

	var navCh <-chan navCommand
	if inputController != nil {
		navCh = inputController.commands
	}

	lines := make(chan logLine, opts.height*2)
	doneCh := make(chan int, 1)
	var readWG sync.WaitGroup
	readWG.Add(2)
	go readLines(stdout, "stdout", lines, &readWG)
	go readLines(stderr, "stderr", lines, &readWG)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start command: %v\n", err)
		return 1
	}

	stopSignals := forwardSignals(cmd)

	go func() {
		readWG.Wait()
		close(lines)
	}()

	go func() {
		err := cmd.Wait()
		stopSignals()
		doneCh <- exitCodeFromError(err)
	}()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	exitCode := 1
	processDone := false
	holdAfterExit := false
	for lines != nil || !processDone || holdAfterExit {
		select {
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			if teeWriter != nil {
				_, _ = fmt.Fprintln(teeWriter, line.text)
			}
			lineCount++
			overwritten := buffer.add(line)
			if !follow && overwritten && viewStart > 0 {
				viewStart--
			}
			renderer.render(statusForCommand(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)
		case nav, ok := <-navCh:
			if !ok {
				navCh = nil
				continue
			}
			viewStart, follow = applyNav(nav, buffer.size, available, viewStart, follow)
			if nav == navQuit && processDone {
				holdAfterExit = false
				continue
			}
			if nav == navFollow && processDone {
				holdAfterExit = false
				continue
			}
			if processDone {
				renderer.render(statusFinishedForCommand(exitCode, follow, holdAfterExit, startedAt, finishedAt, lineCount, available, viewStart), buffer.lines(), viewStart, follow)
			} else {
				renderer.render(statusForCommand(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)
			}
		case code := <-doneCh:
			exitCode = code
			processDone = true
			finishedAt = time.Now()
			holdAfterExit = !follow
			renderer.render(statusFinishedForCommand(exitCode, follow, holdAfterExit, startedAt, finishedAt, lineCount, available, viewStart), buffer.lines(), viewStart, follow)
		case <-ticker.C:
			if !processDone {
				renderer.render(statusForCommand(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)
			}
		}

		if holdAfterExit && navCh == nil {
			holdAfterExit = false
		}
	}

	if opts.clear {
		renderer.clear()
	} else if !holdAfterExit {
		renderer.render(statusFinishedForCommand(exitCode, follow, false, startedAt, finishedAt, lineCount, available, viewStart), buffer.lines(), viewStart, follow)
	}

	return exitCode
}

func runStdinTUI(opts options) int {
	buffer := newRingBuffer(opts.history)
	renderer := newRegionRenderer(os.Stdout, opts.height)
	closeTee, teeWriter, err := setupTeeWriter(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open tee file: %v\n", err)
		return 1
	}
	defer closeTee()

	renderer.reserve()
	follow := true
	viewStart := 0
	lineCount := 0
	startedAt := time.Now()
	finishedAt := startedAt
	available := opts.height - 1
	renderer.render(statusForStdin(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)

	inputController, err := startInputController()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: keyboard navigation disabled: %v\n", err)
		inputController = nil
	}
	if inputController != nil {
		defer inputController.stop()
	}

	var navCh <-chan navCommand
	if inputController != nil {
		navCh = inputController.commands
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	scanCh := make(chan string, opts.height*2)
	scanDone := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			scanCh <- scanner.Text()
		}
		close(scanCh)
		scanDone <- scanner.Err()
	}()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	holdAfterExit := false
	stdinDone := false
	for scanCh != nil || holdAfterExit {
		select {
		case line, ok := <-scanCh:
			if !ok {
				scanCh = nil
				stdinDone = true
				finishedAt = time.Now()
				holdAfterExit = !follow
				renderer.render(statusFinishedForStdin(follow, holdAfterExit, startedAt, finishedAt, lineCount, available, viewStart), buffer.lines(), viewStart, follow)
				continue
			}
			if teeWriter != nil {
				_, _ = fmt.Fprintln(teeWriter, line)
			}
			lineCount++
			overwritten := buffer.add(logLine{stream: "stdin", text: line})
			if !follow && overwritten && viewStart > 0 {
				viewStart--
			}
			renderer.render(statusForStdin(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)
		case nav, ok := <-navCh:
			if !ok {
				navCh = nil
				continue
			}
			viewStart, follow = applyNav(nav, buffer.size, available, viewStart, follow)
			if nav == navQuit && stdinDone {
				holdAfterExit = false
				continue
			}
			if nav == navFollow && stdinDone {
				holdAfterExit = false
				continue
			}
			if stdinDone {
				renderer.render(statusFinishedForStdin(follow, holdAfterExit, startedAt, finishedAt, lineCount, available, viewStart), buffer.lines(), viewStart, follow)
			} else {
				renderer.render(statusForStdin(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)
			}
		case <-ticker.C:
			if !stdinDone {
				renderer.render(statusForStdin(follow, startedAt, lineCount, available, viewStart, time.Now()), buffer.lines(), viewStart, follow)
			}
		}

		if holdAfterExit && navCh == nil {
			holdAfterExit = false
		}
	}

	if err := <-scanDone; err != nil {
		fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
		return 1
	}

	if opts.clear {
		renderer.clear()
	} else if !holdAfterExit {
		renderer.render(statusFinishedForStdin(follow, false, startedAt, finishedAt, lineCount, available, viewStart), buffer.lines(), viewStart, follow)
	}

	return 0
}

func setupTeeWriter(opts options) (func(), io.Writer, error) {
	if opts.tee == "" {
		return func() {}, nil, nil
	}

	flags := os.O_CREATE | os.O_WRONLY
	if opts.append {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(opts.tee, flags, 0644)
	if err != nil {
		return nil, nil, err
	}

	return func() {
		_ = f.Close()
	}, f, nil
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

func (r *regionRenderer) render(status string, lines []logLine, start int, follow bool) {
	rows := make([]string, 0, r.height)
	rows = append(rows, status)

	availableLogs := r.height - 1
	if availableLogs > 0 {
		viewStart, viewEnd, _ := calcWindow(len(lines), availableLogs, start, follow)
		for _, line := range lines[viewStart:viewEnd] {
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
