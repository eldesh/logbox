% LOGBOX(1) logbox 0.1.0

# NAME

logbox - tail-style live log wrapper with scrollback and follow mode

# SYNOPSIS

**logbox** [*flags*] -- <command> [args...]

producer | **logbox** [*flags*]

# DESCRIPTION

**logbox** wraps command output (or piped stdin) and renders a live tail-like view in TUI mode.
It supports FOLLOW and SCROLL modes, scrollback history, optional tee output, and plain passthrough mode.

# FLAGS

**--height** *N*
: Live view height in lines (status line + visible logs).
  Default is auto (about one third of terminal height, min 5, max 30).

**--history** *N*
: Scrollback buffer size in lines for TUI mode.
  Default is 1000. Must be >= **--height**.

**--clear**
: Clear the reserved live region on exit.

**--plain**
: Disable TUI redraw and pass output through as plain logs.

**--tee** *FILE*
: Also write output to *FILE*.

**--append**
: Append to **--tee** file instead of truncating.

# KEY BINDINGS (TUI)

**k** / Up
: Scroll up one line (switches to SCROLL mode).

**j** / Down
: Scroll down one line.

**u** / PageUp
: Scroll up one page.

**d** / PageDown
: Scroll down one page.

**g**
: Jump to top of buffer.

**G**
: Jump to bottom of buffer.

**f**
: Return to FOLLOW mode (tail-follow).

**q** / Enter
: Quit when process has already finished in SCROLL mode.

`Ctrl-C`
: Forward SIGINT to wrapped command while running.

`Ctrl-\`
: Forward SIGQUIT to wrapped command while running.

# EXIT BEHAVIOR

FOLLOW mode
: logbox exits immediately after wrapped process ends.

SCROLL mode
: logbox keeps view open for review. Use **q** or Enter to quit, or **f** to jump to end and quit.

# EXAMPLES

logbox --height 10 -- make test

logbox --height 10 --history 2000 -- make test

logbox --height 14 --history 12000 -- bash -lc 'make lint && make test && make build'

logbox --tee build.log --append -- docker build --progress=plain .

cat app.log | logbox --plain --tee out.log
