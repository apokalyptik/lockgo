// Go port of Stephen J. Friedl's lockrun:
// http://unixwiz.net/tools/lockrun.c
//
// The original lockrun is public domain. This port preserves the same
// command-line interface and advisory-locking model for Unix-like systems.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	lockfile   string
	wait       bool
	sleep      int
	maxtime    int
	verbose    bool
	idempotent bool
}

func main() {
	os.Exit(run())
}

func run() int {
	// The original C program starts its timer before argument parsing and lock
	// acquisition, so --maxtime includes time spent waiting for the lock.
	start := time.Now()

	cfg, command, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	lock, err := os.OpenFile(cfg.lockfile, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot open(%s) [err=%v]\n", cfg.lockfile, err)
		return 1
	}
	defer lock.Close()

	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}

		// LOCK_NB reports contention as EWOULDBLOCK/EAGAIN. Treat other
		// failures as real errors rather than looping forever.
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			fmt.Fprintf(os.Stderr, "ERROR: cannot lock(%s) [err=%v]\n", cfg.lockfile, err)
			return 1
		}

		if !cfg.wait {
			if cfg.idempotent {
				return 0
			}
			fmt.Fprintf(os.Stderr, "ERROR: cannot launch %s - run is locked\n", command[0])
			return 1
		}

		if cfg.verbose {
			fmt.Printf("(locked: sleeping %d secs)\n", cfg.sleep)
		}
		time.Sleep(time.Duration(cfg.sleep) * time.Second)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot exec %s [%v]\n", command[0], err)
		return 1
	}

	if cfg.verbose {
		fmt.Printf("Waiting for process %d\n", cmd.Process.Pid)
	}

	waitErr := cmd.Wait()
	exitCode, rawStatus := processStatus(cmd.ProcessState, waitErr)
	elapsed := int(time.Since(start) / time.Second)

	if cfg.verbose || (cfg.maxtime > 0 && elapsed > cfg.maxtime) {
		fmt.Printf("pid %d exited with status %d, exit code: %d (time=%d sec)\n",
			cmd.Process.Pid, rawStatus, exitCode, elapsed)
	}

	return exitCode
}

func processStatus(state *os.ProcessState, waitErr error) (exitCode, rawStatus int) {
	if state != nil {
		if ws, ok := state.Sys().(syscall.WaitStatus); ok {
			rawStatus = int(ws)
			switch {
			case ws.Exited():
				return ws.ExitStatus(), rawStatus
			case ws.Signaled():
				// Conventional shell-style exit status for a process killed by a signal.
				return 128 + int(ws.Signal()), rawStatus
			}
		}

		if code := state.ExitCode(); code >= 0 {
			return code, rawStatus
		}
	}

	if waitErr != nil {
		return 1, rawStatus
	}
	return 0, rawStatus
}

func parseArgs(args []string) (config, []string, error) {
	cfg := config{sleep: 10}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			command := args[i+1:]
			if len(command) == 0 {
				return cfg, nil, fmt.Errorf("ERROR: missing command to %s (must follow \"--\" marker)", os.Args[0])
			}
			if cfg.lockfile == "" {
				return cfg, nil, errors.New("ERROR: missing --lockfile=F parameter")
			}
			return cfg, command, nil
		}

		name, value, hasValue := strings.Cut(arg, "=")

		getValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("ERROR: option %s requires a parameter", name)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "-L", "--lockfile":
			v, err := getValue()
			if err != nil {
				return cfg, nil, err
			}
			cfg.lockfile = v

		case "-W", "--wait":
			cfg.wait = true

		case "-S", "--sleep":
			v, err := getValue()
			if err != nil {
				return cfg, nil, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return cfg, nil, fmt.Errorf("ERROR: option %s requires a non-negative integer", name)
			}
			cfg.sleep = n

		case "-T", "--maxtime":
			v, err := getValue()
			if err != nil {
				return cfg, nil, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return cfg, nil, fmt.Errorf("ERROR: option %s requires a non-negative integer", name)
			}
			cfg.maxtime = n

		case "-V", "--verbose":
			cfg.verbose = true

		case "-I", "--idempotent":
			cfg.idempotent = true

		default:
			return cfg, nil, fmt.Errorf("ERROR: %q is an invalid cmdline param", name)
		}
	}

	return cfg, nil, fmt.Errorf("ERROR: missing command to %s (must follow \"--\" marker)", os.Args[0])
}
